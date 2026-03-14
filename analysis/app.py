import os
from typing import Dict, Any, List
from flask import Flask, request, jsonify
import pandas as pd
import numpy as np
from sklearn.ensemble import IsolationForest
import psycopg2

app = Flask(__name__)

def get_db_connection() -> psycopg2.extensions.connection:
    db_host = os.getenv("DB_HOST", "postgres") 
    db_port = os.getenv("DB_PORT", "5432")
    db_user = os.getenv("DB_USER", "postgres")
    db_password = os.getenv("DB_PASSWORD", "password123")
    db_name = os.getenv("DB_NAME", "cryptotrace")

    print(f"🔌 [AI Engine] Initializing PostgreSQL connection (Host: {db_host}, User: {db_user})...", flush=True)

    return psycopg2.connect(
        host=db_host,
        port=db_port,
        user=db_user,
        password=db_password,
        dbname=db_name
    )

@app.route('/', methods=['GET'])
def health_check() -> tuple[Dict[str, str], int]:
    return jsonify({"status": "healthy", "service": "CryptoTrace AI Engine"}), 200

@app.route('/analyze', methods=['POST'])
def analyze_wallet_behavior() -> tuple[Dict[str, Any], int]:
    payload = request.json
    target_wallet_address = payload.get('address', '').lower()
    
    # 💡 獲取前端傳遞的時間窗約束 (預設為 0 代表全時段)
    start_time = payload.get('startTime', 0)
    end_time = payload.get('endTime', 0)

    if not target_wallet_address:
        return jsonify({"error": "Missing target address"}), 400

    print(f"\n🔍 [AI Engine] Commencing KYT analysis for wallet: {target_wallet_address}", flush=True)

    conn = get_db_connection()
    cursor = conn.cursor()

    try:
        # =====================================================================
        # PHASE 1: Entity Whitelisting
        # =====================================================================
        cursor.execute("SELECT label FROM wallets WHERE address = %s", (target_wallet_address,))
        row = cursor.fetchone()
        entity_label = row[0] if row else 'wallet'

        if entity_label not in ['wallet', 'HighRisk']:
            print(f"🛡️ [AI Engine] Execution halted: Target is a verified entity ({entity_label}).", flush=True)
            return jsonify({"status": "exempt", "anomalies_found": 0, "anomaly_details": []}), 200

        # =====================================================================
        # PHASE 2: Adaptive Baseline Expansion (自適應基線擴展)
        # Design Decision: 動態決定歷史資料的抓取範圍，避免樣本稀疏與概念漂移。
        # =====================================================================
        MIN_SAMPLES_REQUIRED = 50
        INITIAL_LOOKBACK_DAYS = 30
        MAX_LOOKBACK_DAYS = 90
        
        # 為了計算特徵 (如 24h 滾動頻率)，強迫將查詢起點往前推，作為模型「預熱期」
        training_start_time = start_time - (INITIAL_LOOKBACK_DAYS * 24 * 3600) if start_time > 0 else 0

        def fetch_ego_network(t_start, t_end):
            """內部閉包函式：封裝 SQL 查詢，支援動態時間重試"""
            query = """
                WITH RECURSIVE ego_network AS (
                    SELECT %s::varchar AS address, 0 AS depth
                    UNION
                    SELECT CASE WHEN t.from_address = c.address THEN t.to_address ELSE t.from_address END, c.depth + 1
                    FROM transactions t 
                    JOIN ego_network c ON (t.from_address = c.address OR t.to_address = c.address)
                    JOIN wallets w ON c.address = w.address
                    WHERE c.depth < 2 AND (w.label IN ('wallet', 'HighRisk') OR c.depth = 0)
                    AND (%s::bigint = 0 OR t.timestamp >= %s)
                    AND (%s::bigint = 0 OR t.timestamp <= %s)
                )
                SELECT hash, from_address, to_address, amount, timestamp, type 
                FROM transactions 
                WHERE from_address IN (SELECT address FROM ego_network) 
                   OR to_address IN (SELECT address FROM ego_network)
            """
            import warnings
            # 忽略 pandas 針對 DBAPI2 的警告，保持程式碼乾淨
            with warnings.catch_warnings():
                warnings.simplefilter('ignore', UserWarning)
                return pd.read_sql(query, conn, params=(
                    target_wallet_address, 
                    t_start, t_start, 
                    t_end, t_end
                ))

        # 第一次嘗試：抓取 30 天的預熱資料
        local_context_tx_df = fetch_ego_network(training_start_time, end_time)

        # 核心防禦：如果 30 天內資料太少，自適應擴展到 90 天
        if start_time > 0 and len(local_context_tx_df) < MIN_SAMPLES_REQUIRED:
            print(f"⚠️ [AI Engine] Sparse data ({len(local_context_tx_df)} edges). Expanding lookback to {MAX_LOOKBACK_DAYS} days...", flush=True)
            extended_start_time = start_time - (MAX_LOOKBACK_DAYS * 24 * 3600)
            local_context_tx_df = fetch_ego_network(extended_start_time, end_time)

        if local_context_tx_df.empty or len(local_context_tx_df) < 5:
            print(f"🛑 [AI Engine] Insufficient graph density for ML. Halting.", flush=True)
            return jsonify({"status": "insufficient_data", "anomalies_found": 0, "anomaly_details": []}), 200

        # =====================================================================
        # PHASE 3: Feature Engineering 
        # =====================================================================
        local_context_tx_df = local_context_tx_df.sort_values(by=['from_address', 'timestamp']).reset_index(drop=True)
        local_context_tx_df['datetime'] = pd.to_datetime(local_context_tx_df['timestamp'], unit='s')
        local_context_tx_df['time_diff'] = local_context_tx_df.groupby('from_address')['timestamp'].diff().fillna(0)
        
        df_time_indexed = local_context_tx_df.set_index('datetime')
        rolling_frequency_series = df_time_indexed.groupby('from_address')['amount'].rolling('24h').count()
        local_context_tx_df['tx_freq_24h'] = rolling_frequency_series.reset_index(level=0, drop=True).values
        
        feature_columns = ['amount', 'time_diff', 'tx_freq_24h']
        baseline_feature_matrix = local_context_tx_df[feature_columns].values 

        # =====================================================================
        # PHASE 4: Unsupervised Learning (Isolation Forest)
        # =====================================================================
        isolation_forest_model = IsolationForest(contamination='auto', random_state=42) 
        isolation_forest_model.fit(baseline_feature_matrix)

        # 計算整個預熱期 + 時間窗的網路動態天花板
        amount_95th = np.percentile(local_context_tx_df['amount'], 95)
        freq_95th = np.percentile(local_context_tx_df['tx_freq_24h'], 95)

        # =====================================================================
        # PHASE 5: Temporal Slicing & XAI Verification (時序切割與可解釋性 AI)
        # Design Decision: 訓練完成後，我們只把「使用者真正關心的時間窗」切出來做推論。
        # =====================================================================
        target_wallet_mask = (local_context_tx_df['from_address'] == target_wallet_address) | (local_context_tx_df['to_address'] == target_wallet_address)
        target_tx_df = local_context_tx_df[target_wallet_mask].copy()

        # 💡 嚴格剔除預熱期的資料，只留下使用者指定的時間窗
        if start_time > 0:
            target_tx_df = target_tx_df[target_tx_df['timestamp'] >= start_time]
        if end_time > 0:
            target_tx_df = target_tx_df[target_tx_df['timestamp'] <= end_time]
        
        if target_tx_df.empty:
            print("✅ [AI Engine] Target wallet has no activity within the specified time window.", flush=True)
            return jsonify({"status": "no_target_data_in_window", "anomalies_found": 0, "anomaly_details": []}), 200

        target_feature_matrix = target_tx_df[feature_columns].values
        
        # 👑 唯一的判斷標準：讓 ML 模型說了算
        target_tx_df['ai_label'] = isolation_forest_model.predict(target_feature_matrix)
        target_tx_df['anomaly_score'] = isolation_forest_model.decision_function(target_feature_matrix) 

        def extract_compliance_reasons(row: pd.Series) -> List[str]:
            reasons = []
            
            # 第一道防線：模型沒有標記為 Outlier (-1)，直接放行
            if row['ai_label'] != -1: 
                return reasons
            
            # 灰塵過濾 (Dusting Filter)：忽略 3000 U 以下的微小雜訊
            if row['amount'] < 3000: 
                return reasons 
                
            # XAI 翻譯層：向法遵人員解釋模型可能看到了什麼特徵
            if row['amount'] > amount_95th:
                reasons.append(f"ML Insight: Volume ({row['amount']:.2f} U) exceeds network 95th percentile")
            
            if 0 < row['time_diff'] < 60:
                reasons.append(f"ML Insight: Bot-like high-velocity transfer ({int(row['time_diff'])}s)")
                
            if row['tx_freq_24h'] > freq_95th:
                reasons.append(f"ML Insight: Frequency ({int(row['tx_freq_24h'])} txs/24h) exceeds network 95th percentile")
            
            # 邊緣情況：若單一維度都沒破 95%，代表這是多維度結構性異常
            if not reasons:
                reasons.append(f"ML Insight: Multi-dimensional structural anomaly (Score: {row['anomaly_score']:.3f})")

            return reasons

        target_tx_df['compliance_reasons'] = target_tx_df.apply(extract_compliance_reasons, axis=1)
        target_tx_df['is_verified_anomaly'] = target_tx_df['compliance_reasons'].apply(lambda x: len(x) > 0)
        verified_anomalies_count = int(target_tx_df['is_verified_anomaly'].sum())
        
        # =====================================================================
        # PHASE 6: State Mutation & Reporting
        # =====================================================================
        compliance_report = []

        if verified_anomalies_count > 0:
            anomalous_transactions = target_tx_df[target_tx_df['is_verified_anomaly']]
            
            for _, row in anomalous_transactions.iterrows():
                compliance_report.append({
                    "tx_hash": row['hash'],
                    "amount": float(row['amount']),
                    "timestamp": int(row['timestamp']),
                    "reasons": row['compliance_reasons']
                })

            update_risk_query = "UPDATE wallets SET label = 'HighRisk' WHERE address = %s AND label = 'wallet'"
            cursor.execute(update_risk_query, (target_wallet_address,))
            conn.commit()
            
            print(f"🚨 [AI] Classification Complete: {verified_anomalies_count} illicit signatures detected. Entity {target_wallet_address} marked as HighRisk.", flush=True)
            for detail in compliance_report:
                reason_str = " | ".join(detail['reasons'])
                print(f"   👉 Tx: {detail['tx_hash']} | Amount: {detail['amount']:,.2f} U | Triggers: {reason_str}", flush=True)
        else:
            print(f"✅ [AI] Classification Complete: Normal behavioral distribution (0/{len(target_tx_df)} anomalies).", flush=True)

        return jsonify({
            "status": "analyzed", 
            "network_baseline_txs": len(local_context_tx_df),
            "target_txs_analyzed": len(target_tx_df),
            "anomalies_found": verified_anomalies_count,
            "anomaly_details": compliance_report 
        }), 200

    except Exception as e:
        print(f"❌ [AI Engine] Critical failure during ML pipeline execution: {e}", flush=True)
        return jsonify({"error": "Internal AI Engine Failure", "details": str(e)}), 500

    finally:
        cursor.close()
        conn.close()

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=8080)