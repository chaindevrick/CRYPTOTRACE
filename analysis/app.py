import os
from flask import Flask, request, jsonify
import pandas as pd
import numpy as np
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

    # ==========================================
    # 🛡️ 1. 白名單豁免機制 (Entity Exemption)
    # ==========================================
    cursor.execute("SELECT label FROM wallets WHERE address = %s", (target_address,))
    row = cursor.fetchone()
    wallet_label = row[0] if row else 'wallet'

    if wallet_label not in ['wallet', 'HighRisk']:
        print(f"🛡️ [AI Engine] 目標為已知機構或合約 ({wallet_label})，具備白名單豁免權，跳過 AI 檢測。", flush=True)
        cursor.close()
        conn.close()
        return jsonify({"status": "exempt", "anomalies_found": 0})

    # ==========================================
    # 🕸️ 2. 獲取局部網路基準資料 (Baseline Data)
    # ==========================================
    # 預設撈取目標錢包及其關聯交易。
    query = """
        WITH RECURSIVE ego_network AS (
            -- 第 0 層：目標錢包本身
            SELECT %s::varchar AS address, 0 AS depth
            UNION
            -- 第 1 到 3 層：不斷找出與上一層有交易往來的新錢包
            SELECT 
                CASE 
                    WHEN t.from_address = n.address THEN t.to_address 
                    ELSE t.from_address 
                END, 
                n.depth + 1
            FROM ego_network n
            JOIN transactions t ON n.address = t.from_address OR n.address = t.to_address
            WHERE n.depth < 3  -- 往外擴展 3 層
        )
        -- 最後，把這個三層交友圈內發生的「所有交易」全部撈出來作為基準
        SELECT from_address, to_address, amount, timestamp, type 
        FROM transactions 
        WHERE from_address IN (SELECT address FROM ego_network) 
           OR to_address IN (SELECT address FROM ego_network)
    """

    df = pd.read_sql(query, conn, params=(target_address,))
    df = pd.read_sql(query, conn, params=(target_address, target_address))

    if df.empty or len(df) < 5:
        print(f"⚠️ [AI Engine] 資料量不足 ({len(df)} 筆)，跳過機器學習分析。", flush=True)
        cursor.close()
        conn.close()
        return jsonify({"status": "insufficient_data", "anomalies_found": 0})

    print(f"📊 [AI Engine] 成功從資料庫讀取 {len(df)} 筆交易，準備萃取多維度特徵...", flush=True)

    # ==========================================
    # 🌟 3. 特徵工程 (Feature Engineering: 金額 + 時間 + 頻率)
    # ==========================================
    # 確保時間戳記格式正確並依時間排序 (計算時間差必須先排序)
    df = df.sort_values(by='timestamp').reset_index(drop=True)
    df['datetime'] = pd.to_datetime(df['timestamp'], unit='s')

    # [特徵 A] 時間：計算與上一筆交易的時間間隔 (秒數)
    df['time_diff'] = df['timestamp'].diff().fillna(0)

    # [特徵 B] 頻率：計算過去 24 小時內的交易次數 (Rolling Window)
    df_indexed = df.set_index('datetime')
    df['tx_freq_24h'] = df_indexed['amount'].rolling('24h').count().values
    
    # 定義多維度輸入特徵 X
    features = ['amount', 'time_diff', 'tx_freq_24h']
    X_baseline = df[features].values 
    
    # 計算局部網路的日常交易中位數，用於後續比對
    median_val = df['amount'].median()

    # ==========================================
    # 🌲 4. 機器學習：訓練局部生態的孤立森林
    # ==========================================
    # contamination=0.05 代表我們假設這個局部生態中大約有 5% 的交易是異常的
    clf = IsolationForest(contamination=0.05, random_state=42) 
    
    # 模型只進行 fit，學習這個「局部網路 (Ego-network)」的正常標準
    clf.fit(X_baseline)

    # ==========================================
    # 🎯 5. 異常檢測：將目標錢包的交易放入森林計算
    # ==========================================
    # 篩選出「只屬於目標錢包」發出或接收的交易進行評分
    target_mask = (df['from_address'] == target_address) | (df['to_address'] == target_address)
    df_target = df[target_mask].copy()
    
    if df_target.empty:
        cursor.close()
        conn.close()
        return jsonify({"status": "no_target_data", "anomalies_found": 0})

    # 對目標錢包的交易進行預測與異常分數計算
    X_target = df_target[features].values
    df_target['ai_label'] = clf.predict(X_target)
    df_target['anomaly_score'] = clf.decision_function(X_target) 

    # ==========================================
    # ⚖️ 6. 雙重確認邏輯 (Double Verification)
    # ==========================================
    def is_true_anomaly(row):
        # A. 模型必須判定為異常 (-1 代表異常，1 代表正常)
        if row['ai_label'] != -1:
            return False
            
        # B. 絕對金額門檻 (過濾掉小額測試或 Gas Fee，避免誤報)
        if row['amount'] < 3000:
            return False
            
        # C. 洗錢特徵判斷 (滿足以下任一業務邏輯即視為高風險)
        is_amount_spike = (row['amount'] > median_val * 5)               # 金額突增：偏離日常習慣 5 倍
        is_rapid_tx = (row['time_diff'] < 60 and row['time_diff'] > 0)   # 機器人特徵：間隔小於 60 秒的連發
        is_high_freq = (row['tx_freq_24h'] > 20)                         # 高頻特徵：24 小時內單一節點交易超過 20 次
        
        # 只要模型認為是異常，且符合上述任何一個具體的洗錢特徵，就發報
        if is_amount_spike or is_rapid_tx or is_high_freq:
            return True
            
        return False

    df_target['is_true_anomaly'] = df_target.apply(is_true_anomaly, axis=1)
    anomalies_found = int(df_target['is_true_anomaly'].sum())
    
    # ==========================================
    # 🚨 7. 警報與狀態更新
    # ==========================================
    if anomalies_found > 0:
        print(f"🚨 [AI Engine] 警報！在局部生態中發現 {anomalies_found} 筆目標錢包的異常洗錢行為！", flush=True)
        
        anomalous_amounts = df_target[df_target['is_true_anomaly'] == True]['amount'].tolist()
        print(f"   👉 異常交易金額: {anomalous_amounts[:5]}... (局部網路中位數: {median_val:.2f})", flush=True)

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
        print(f"✅ [AI Engine] 分析完成，目標錢包行為符合此局部網路之常態，未觸發警報 (0/{len(df_target)})。", flush=True)

    cursor.close()
    conn.close()
    return jsonify({
        "status": "analyzed", 
        "network_baseline_txs": len(df),
        "target_txs_analyzed": len(df_target),
        "anomalies_found": anomalies_found
    })

if __name__ == '__main__':
    app.run(host='0.0.0.0', port=5001)