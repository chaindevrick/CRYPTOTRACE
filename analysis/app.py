import os
from typing import Dict, Any, List
from flask import Flask, request, jsonify
import pandas as pd
import numpy as np
from sklearn.ensemble import IsolationForest
import psycopg2

app = Flask(__name__)

def get_db_connection() -> psycopg2.extensions.connection:
    """
    建立並回傳 PostgreSQL 資料庫連線。
    
    Design Decision: 捨棄單一 DSN (Data Source Name) 字串，改採獨立參數傳遞。
    Why: 避免當密碼 (DB_PASSWORD) 包含特殊保留字元 (@, /, ?) 時，
         引發 URL 解析錯誤，同時完美相容 GCP Cloud Run 的 Unix Socket 路徑格式。
    """
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
    """
    Liveliness Probe 端點。
    Why: 專為 GCP Cloud Run / Kubernetes 負載平衡器設計，用於確認 Container 啟動完畢且可接收流量。
    """
    return jsonify({"status": "healthy", "service": "CryptoTrace AI Engine"}), 200

@app.route('/analyze', methods=['POST'])
def analyze_wallet_behavior() -> tuple[Dict[str, Any], int]:
    """
    核心鑑識端點：基於局部生態網路 (Ego-Network) 的孤立森林異常檢測引擎。
    """
    payload = request.json
    target_wallet_address = payload.get('address', '').lower()

    if not target_wallet_address:
        return jsonify({"error": "Missing target address"}), 400

    print(f"\n🔍 [AI Engine] Commencing KYT analysis for wallet: {target_wallet_address}", flush=True)

    conn = get_db_connection()
    cursor = conn.cursor()

    try:
        # =====================================================================
        # PHASE 1: Entity Whitelisting & Stop-Loss
        # Design Decision: 優先檢查實體標籤，若為交易所 (CEX) 或已知高風險，直接繞過 AI 推論。
        # Why: 
        #   1. 節省算力 (Compute Optimization)。
        #   2. 避免超級節點 (Supernodes) 的巨量交易數據污染機器學習模型的常態分佈基準。
        # =====================================================================
        cursor.execute("SELECT label FROM wallets WHERE address = %s", (target_wallet_address,))
        row = cursor.fetchone()
        entity_label = row[0] if row else 'wallet'

        if entity_label not in ['wallet', 'HighRisk']:
            print(f"🛡️ [AI Engine] Execution halted: Target is a verified entity ({entity_label}).", flush=True)
            return jsonify({"status": "exempt", "anomalies_found": 0, "anomaly_details": []}), 200

        # =====================================================================
        # PHASE 2: Local Context (Ego-Network) Retrieval
        # Design Decision: 利用 Recursive CTE 抓取 2-Hop 內的交易，而非使用全網資料訓練。
        # Why: 加密貨幣交易具備高度的「群聚效應」。全網的常態基準無法反映特定局部網路
        #      (例如某個特定的 DeFi 礦池社群) 的真實日常行為，局部基準能有效降低 False Positives。
        # =====================================================================
        ego_network_query = """
            WITH RECURSIVE ego_network AS (
                SELECT %s::varchar AS address, 0 AS depth
                UNION
                SELECT 
                    CASE WHEN t.from_address = c.address THEN t.to_address ELSE t.from_address END, 
                    c.depth + 1
                FROM transactions t 
                JOIN ego_network c ON (t.from_address = c.address OR t.to_address = c.address)
                JOIN wallets w ON c.address = w.address
                WHERE c.depth < 2 AND (w.label IN ('wallet', 'HighRisk') OR c.depth = 0)
            )
            SELECT hash, from_address, to_address, amount, timestamp, type 
            FROM transactions 
            WHERE from_address IN (SELECT address FROM ego_network) 
               OR to_address IN (SELECT address FROM ego_network)
        """

        local_context_tx_df = pd.read_sql(ego_network_query, conn, params=(target_wallet_address,))

        # 防禦性設計：解決 Burner Wallet (免洗錢包) 的冷啟動問題
        if local_context_tx_df.empty or len(local_context_tx_df) < 5:
            print(f"⚠️ [AI Engine] Insufficient graph density ({len(local_context_tx_df)} edges). Fallback to heuristics.", flush=True)
            return jsonify({"status": "insufficient_data", "anomalies_found": 0, "anomaly_details": []}), 200

        # =====================================================================
        # PHASE 3: Feature Engineering (Behavioral Vectorization)
        # Design Decision: 將一維的「轉帳金額」轉換為三維的「行為時序向量」。
        # Why: 現代混幣器 (Mixers) 或碎星洗錢 (Smurfing) 通常會將金額打散，
        #      單看金額無法察覺異常。我們引入「時間差」與「滾動頻率」來捕捉程式化機器人的特徵。
        # =====================================================================
        # 確保時序正確，避免時間差計算錯誤
        local_context_tx_df = local_context_tx_df.sort_values(by=['from_address', 'timestamp']).reset_index(drop=True)
        local_context_tx_df['datetime'] = pd.to_datetime(local_context_tx_df['timestamp'], unit='s')

        # 擷取特徵 1：同一發送者的連續交易時間差 (秒)
        local_context_tx_df['time_diff'] = local_context_tx_df.groupby('from_address')['timestamp'].diff().fillna(0)
        
        # 擷取特徵 2：24 小時滾動時間窗內的交易頻率
        df_time_indexed = local_context_tx_df.set_index('datetime')
        rolling_frequency_series = df_time_indexed.groupby('from_address')['amount'].rolling('24h').count()
        local_context_tx_df['tx_freq_24h'] = rolling_frequency_series.reset_index(level=0, drop=True).values
        
        feature_columns = ['amount', 'time_diff', 'tx_freq_24h']
        baseline_feature_matrix = local_context_tx_df[feature_columns].values 
        network_median_amount = local_context_tx_df['amount'].median()

        # =====================================================================
        # PHASE 4: Unsupervised Learning (Isolation Forest)
        # Design Decision: 使用 Isolation Forest 並將 contamination 設為 auto。
        # Why: 相較於 DBSCAN 或 K-Means，Isolation Forest 更擅長在高維空間中切割邊緣資料點 (Outliers)。
        #      'auto' 允許模型依據局部網路的實際密度動態決定決策樹的深度，而非寫死異常比例。
        # =====================================================================
        isolation_forest_model = IsolationForest(contamination='auto', random_state=42) 
        isolation_forest_model.fit(baseline_feature_matrix)

        # =====================================================================
        # PHASE 5: Explainable AI (XAI) & Dual-Track Verification
        # Design Decision: 模型白箱化 (White-boxing) 與雙重確認機制。
        # Why: 非監督式學習的黑箱輸出 (1 / -1) 無法作為法遵人員凍結資金的證據 (Lack of legal evidence)。
        #      我們利用領域專家規則 (Heuristics) 將觸發條件具象化，只有在模型判斷異常，
        #      且滿足具體洗錢特徵時，才確立為 True Anomaly。
        # =====================================================================
        target_wallet_mask = (local_context_tx_df['from_address'] == target_wallet_address) | (local_context_tx_df['to_address'] == target_wallet_address)
        target_tx_df = local_context_tx_df[target_wallet_mask].copy()
        
        if target_tx_df.empty:
            return jsonify({"status": "no_target_data", "anomalies_found": 0, "anomaly_details": []}), 200

        target_feature_matrix = target_tx_df[feature_columns].values
        target_tx_df['ai_label'] = isolation_forest_model.predict(target_feature_matrix)
        # 保留分數供未來動態權重調整使用
        target_tx_df['anomaly_score'] = isolation_forest_model.decision_function(target_feature_matrix) 

        def extract_compliance_reasons(row: pd.Series) -> List[str]:
            """將數學異常轉換為具備合規證據力的具體特徵描述"""
            reasons = []
            
            # 第一道防線：模型必須先認為是 Outlier (-1)
            if row['ai_label'] != -1: 
                return reasons
            
            # 灰塵過濾 (Dusting Filter)：忽略微小金額的雜訊攻擊
            if row['amount'] < 3000: 
                return reasons 
                
            # 第二道防線：特徵維度具象化
            if row['amount'] > network_median_amount * 5:
                reasons.append(f"Volume Surge: {row['amount']:.2f} U (5x above local median)")
            if 0 < row['time_diff'] < 60:
                reasons.append(f"Bot Activity: High-velocity transfer within {int(row['time_diff'])}s")
            if row['tx_freq_24h'] > 20:
                reasons.append(f"Frequency Spike: {int(row['tx_freq_24h'])} txs in rolling 24h")
                
            return reasons

        target_tx_df['compliance_reasons'] = target_tx_df.apply(extract_compliance_reasons, axis=1)
        target_tx_df['is_verified_anomaly'] = target_tx_df['compliance_reasons'].apply(lambda x: len(x) > 0)
        
        verified_anomalies_count = int(target_tx_df['is_verified_anomaly'].sum())
        
        # =====================================================================
        # PHASE 6: State Mutation & Reporting
        # Design Decision: 將異常結果寫回關聯式資料庫，作為後續前端拓撲圖 (Graph) 的節點屬性。
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

            # 更新節點風險等級
            update_risk_query = "UPDATE wallets SET label = 'HighRisk' WHERE address = %s AND label = 'wallet'"
            cursor.execute(update_risk_query, (target_wallet_address,))
            conn.commit()
            
            print(f"🚨 [AI] Classification Complete: {verified_anomalies_count} illicit signatures detected. Entity {target_wallet_address} marked as HighRisk.", flush=True)
            for detail in compliance_report:
                reason_str = " | ".join(detail['reasons'])
                print(f"   👉 Tx: {detail['tx_hash'][:12]}... | Amount: {detail['amount']:,.2f} U | Triggers: {reason_str}", flush=True)
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
        # 防禦性設計：確保連線資源不會因為中途拋出 Exception 而產生 Memory Leak
        print(f"❌ [AI Engine] Critical failure during ML pipeline execution: {e}", flush=True)
        return jsonify({"error": "Internal AI Engine Failure", "details": str(e)}), 500

    finally:
        cursor.close()
        conn.close()

if __name__ == '__main__':
    # 建議正式環境改用 Gunicorn: 
    # gunicorn --bind 0.0.0.0:8080 --workers 1 --threads 8 app:app
    app.run(host='0.0.0.0', port=8080)