import os
from flask import Flask, request, jsonify
import pandas as pd
import numpy as np
from sklearn.ensemble import IsolationForest
import psycopg2

app = Flask(__name__)

def get_db_connection():
    db_host = os.getenv("DB_HOST") 
    db_port = "5432"
    db_user = "postgres"
    db_password = os.getenv("DB_PASSWORD")
    db_name = "cryptotrace"

    print(f"🔌 [AI Engine] 正在連線至 PostgreSQL (Host: {db_host}, User: {db_user})...", flush=True)

    # 💡 psycopg2 支援直接傳入參數 (Keyword Arguments)
    # 這樣做最安全，完全不用擔心密碼有特殊符號，也不用管 Unix Socket 路徑格式！
    return psycopg2.connect(
        host=db_host,
        port=db_port,
        user=db_user,
        password=db_password,
        dbname=db_name
    )

@app.route('/', methods=['GET'])
def health_check():
    return jsonify({"status": "healthy", "service": "CryptoTrace AI Engine"}), 200

@app.route('/analyze', methods=['POST'])
def analyze():
    data = request.json
    target_address = data.get('address').lower()

    print(f"\n🔍 [AI Engine] 收到分析請求，目標錢包: {target_address}", flush=True)

    conn = get_db_connection()
    cursor = conn.cursor()

    try:
        # ==========================================
        # 🛡️ 1. 白名單豁免機制
        # ==========================================
        cursor.execute("SELECT label FROM wallets WHERE address = %s", (target_address,))
        row = cursor.fetchone()
        wallet_label = row[0] if row else 'wallet'

        if wallet_label not in ['wallet', 'HighRisk']:
            print(f"🛡️ [AI Engine] 目標為已知機構或合約 ({wallet_label})，跳過 AI 檢測。", flush=True)
            return jsonify({"status": "exempt", "anomalies_found": 0, "anomaly_details": []})

        # ==========================================
        # 🕸️ 2. 獲取局部網路基準資料 
        # ==========================================
        query = """
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

        df = pd.read_sql(query, conn, params=(target_address,))

        if df.empty or len(df) < 5:
            print(f"⚠️ [AI Engine] 資料量不足 ({len(df)} 筆)，跳過機器學習分析。", flush=True)
            return jsonify({"status": "insufficient_data", "anomalies_found": 0, "anomaly_details": []})

        # ==========================================
        # 🌟 3. 特徵工程 (Feature Engineering)
        # ==========================================
        df = df.sort_values(by=['from_address', 'timestamp']).reset_index(drop=True)
        df['datetime'] = pd.to_datetime(df['timestamp'], unit='s')

        df['time_diff'] = df.groupby('from_address')['timestamp'].diff().fillna(0)
        
        df_indexed = df.set_index('datetime')
        freq_series = df_indexed.groupby('from_address')['amount'].rolling('24h').count()
        df['tx_freq_24h'] = freq_series.reset_index(level=0, drop=True).values
        
        features = ['amount', 'time_diff', 'tx_freq_24h']
        X_baseline = df[features].values 
        median_val = df['amount'].median()

        # ==========================================
        # 🌲 4. 機器學習：訓練局部生態的孤立森林
        # ==========================================
        clf = IsolationForest(contamination='auto', random_state=42) 
        clf.fit(X_baseline)

        # ==========================================
        # 🎯 5. 異常檢測與白盒化解釋 (Explainable AI)
        # ==========================================
        target_mask = (df['from_address'] == target_address) | (df['to_address'] == target_address)
        df_target = df[target_mask].copy()
        
        if df_target.empty:
            return jsonify({"status": "no_target_data", "anomalies_found": 0, "anomaly_details": []})

        X_target = df_target[features].values
        df_target['ai_label'] = clf.predict(X_target)
        df_target['anomaly_score'] = clf.decision_function(X_target) 

        # ✨ 升級：將布林值檢查改為收集「異常理由字串」
        def get_anomaly_reasons(row):
            reasons = []
            if row['ai_label'] != -1: return reasons
            if row['amount'] < 3000: return reasons 
                
            if row['amount'] > median_val * 5:
                reasons.append(f"金額暴增 (達 {row['amount']:.2f} U，超過日常中位數的 5 倍)")
            if row['time_diff'] < 60 and row['time_diff'] > 0:
                reasons.append(f"機器人特徵：短於 {int(row['time_diff'])} 秒的連續轉帳")
            if row['tx_freq_24h'] > 20:
                reasons.append(f"高頻交易異常 (24小時內達 {int(row['tx_freq_24h'])} 次)")
                
            return reasons

        df_target['anomaly_reasons'] = df_target.apply(get_anomaly_reasons, axis=1)
        # 只要 reasons 陣列裡面有東西，這筆交易就是真正的洗錢交易
        df_target['is_true_anomaly'] = df_target['anomaly_reasons'].apply(lambda x: len(x) > 0)
        anomalies_found = int(df_target['is_true_anomaly'].sum())
        
        # ==========================================
        # 🚨 6. 警報與狀態更新 (匯出白盒化報告)
        # ==========================================
        anomaly_details = []

        if anomalies_found > 0:
            # 提取所有異常交易的詳細資訊
            anomalous_rows = df_target[df_target['is_true_anomaly']]
            for _, row in anomalous_rows.iterrows():
                anomaly_details.append({
                    "tx_hash": row['hash'],
                    "amount": float(row['amount']),
                    "timestamp": int(row['timestamp']),
                    "reasons": row['anomaly_reasons']
                })

            update_query = "UPDATE wallets SET label = 'HighRisk' WHERE address = %s AND label = 'wallet'"
            cursor.execute(update_query, (target_address,))
            conn.commit()
            
            print(f"🚨 [AI] 發現 {anomalies_found} 筆異常！已將 {target_address} 標記為 HighRisk。", flush=True)
            # 在 Log 中印出詳細的犯罪報告
            for detail in anomaly_details:
                reason_str = " | ".join(detail['reasons'])
                print(f"   👉 Tx: {detail['tx_hash'][:12]}... | 金額: {detail['amount']:,.2f} U | 原因: {reason_str}", flush=True)
        else:
            print(f"✅ [AI] 分析完成，行為符合常態 (0/{len(df_target)})。", flush=True)

        # 將異常細節一起透過 JSON 回傳給前端
        return jsonify({
            "status": "analyzed", 
            "network_baseline_txs": len(df),
            "target_txs_analyzed": len(df_target),
            "anomalies_found": anomalies_found,
            "anomaly_details": anomaly_details 
        })

    except Exception as e:
        print(f"❌ [AI Engine] 發生嚴重錯誤: {e}", flush=True)
        return jsonify({"error": str(e)}), 500

    finally:
        cursor.close()
        conn.close()

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=8080)