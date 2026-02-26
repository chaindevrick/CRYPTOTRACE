import os
from flask import Flask, request, jsonify
import pandas as pd
from sklearn.ensemble import IsolationForest
import psycopg2

app = Flask(__name__)

def get_db_connection():
    db_url = os.getenv("DATABASE_URL", "postgresql://postgres:password123@postgres:5432/cryptotrace")
    return psycopg2.connect(db_url)

@app.route('/analyze', methods=['POST'])
def analyze():
    data = request.json
    target_address = data.get('address').lower()

    print(f"\n🔍 [AI Engine] 收到分析請求，目標錢包: {target_address}", flush=True)

    conn = get_db_connection()
    cursor = conn.cursor()

    # ✨ 升級 1：白名單豁免機制 (Entity Exemption)
    # 先查這個錢包在資料庫裡是不是已經被解析為交易所或智能合約
    cursor.execute("SELECT label FROM wallets WHERE address = %s", (target_address,))
    row = cursor.fetchone()
    wallet_label = row[0] if row else 'wallet'

    if wallet_label != 'wallet' and wallet_label != 'HighRisk':
        print(f"🛡️ [AI Engine] 目標為已知機構或合約 ({wallet_label})，具備白名單豁免權，跳過 AI 檢測。", flush=True)
        cursor.close()
        conn.close()
        return jsonify({"status": "exempt", "anomalies_found": 0})

    query = """
        SELECT amount, timestamp, type 
        FROM transactions 
        WHERE from_address = %s OR to_address = %s
    """
    df = pd.read_sql(query, conn, params=(target_address, target_address))

    if df.empty or len(df) < 5:
        print(f"⚠️ [AI Engine] 資料量不足 ({len(df)} 筆)，跳過機器學習分析。", flush=True)
        cursor.close()
        conn.close()
        return jsonify({"status": "insufficient_data", "anomalies_found": 0})

    print(f"📊 [AI Engine] 成功從資料庫讀取 {len(df)} 筆交易，準備萃取特徵...", flush=True)

    X = df[['amount']].values 
    median_val = df['amount'].median()

    clf = IsolationForest(contamination='auto', random_state=42)
    df['ai_label'] = clf.fit_predict(X)
    df['anomaly_score'] = clf.decision_function(X) 

    # ✨ 升級 2：散戶投資防呆機制 (Retail Investor Safeguard)
    def is_true_anomaly(row):
        # A: 模型判定異常
        if row['ai_label'] != -1:
            return False
            
        # B: 異常分數門檻
        if row['anomaly_score'] > -0.05:
            return False
            
        # C: 倍數門檻 (偏離日常習慣至少 5 倍)
        if not (row['amount'] > median_val * 5 or row['amount'] < median_val / 5):
            return False
            
        # D: 法規絕對金額門檻 (Absolute AML Threshold) 👈 解決小資族轉帳被誤殺的關鍵
        # 即使你平常轉 20U，今天領薪水轉了 1000U 去幣安，也不會被標記。
        # 我們將洗錢特徵的底線設定為單筆 3000 U (可依需求調整)。
        if row['amount'] < 3000:
            return False
            
        return True

    df['is_true_anomaly'] = df.apply(is_true_anomaly, axis=1)
    anomalies_found = int(df['is_true_anomaly'].sum())
    
    if anomalies_found > 0:
        print(f"🚨 [AI Engine] 警報！通過雙重確認，發現 {anomalies_found} 筆大額洗錢特徵！", flush=True)
        
        anomalous_amounts = df[df['is_true_anomaly'] == True]['amount'].tolist()
        print(f"   👉 異常交易金額: {anomalous_amounts[:5]}... (錢包日常中位數: {median_val:.2f})", flush=True)

        # 寫入 HighRisk 標籤
        update_query = """
            UPDATE wallets 
            SET label = 'HighRisk' 
            WHERE address = %s AND label = 'wallet'
        """
        cursor.execute(update_query, (target_address,))
        conn.commit()
        print(f"🏷️ [AI Engine] 已將一般錢包 {target_address} 升級標記為 'HighRisk'。", flush=True)
    else:
        print(f"✅ [AI Engine] 分析完成，此錢包行為符合散戶或正常邏輯，未觸發洗錢警報 (0/{len(df)})。", flush=True)

    cursor.close()
    conn.close()
    return jsonify({
        "status": "analyzed", 
        "total_txs": len(df),
        "anomalies_found": anomalies_found
    })

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=5000)