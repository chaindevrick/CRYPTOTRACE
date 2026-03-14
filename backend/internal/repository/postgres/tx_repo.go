package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"backend/internal/domain"
)

// =====================================================================
// Repository Layer: Transaction & Graph Persistence
// Design Decision: 實作 domain.TransactionRepository。
// Why: 將複雜的 SQL 語法、遞迴查詢 (CTE) 與資料列映射 (Row Scanning) 
//      完全封裝於此。業務邏輯層 (Usecase) 只需要呼叫 GetGraph()，
//      完全不需要知道底層是怎麼撈出這 250 條連線的。
// =====================================================================

type txRepository struct {
	db *sql.DB
}

// NewTransactionRepository 透過依賴注入 (DI) 接收連線池
func NewTransactionRepository(db *sql.DB) domain.TransactionRepository {
	return &txRepository{db: db}
}

// =====================================================================
// [Method] UpsertTx: 冪等性寫入交易與錢包節點
// Design Decision: 引入 SQL Transaction (tx.ExecContext) 保證 ACID 特性。
// Why: 區塊鏈資料具有高度關聯性。如果「發送方錢包」寫入成功，但「交易紀錄」
//      寫入失敗，資料庫就會出現髒資料。使用 Transaction 確保這三個 Insert
//      動作具備原子性 (Atomicity)。
// =====================================================================
func (r *txRepository) UpsertTx(ctx context.Context, from, to, hash, token, txType string, amount float64, timestamp int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("開啟資料庫交易失敗: %w", err)
	}
	// 防禦性設計：確保中途出錯時能回滾 (Rollback)
	defer tx.Rollback()

	// 1. 確保錢包節點存在 (ON CONFLICT DO NOTHING)
	walletQuery := `
        INSERT INTO wallets (address, label) VALUES ($1, 'wallet')
        ON CONFLICT (address) DO NOTHING
    `
	if _, err := tx.ExecContext(ctx, walletQuery, from); err != nil {
		return fmt.Errorf("寫入發送方錢包失敗: %w", err)
	}
	if _, err := tx.ExecContext(ctx, walletQuery, to); err != nil {
		return fmt.Errorf("寫入接收方錢包失敗: %w", err)
	}

	// 2. 寫入交易邊 (Edge)，若重複則更新其狀態 (如升級為 Trace 模式)
	txQuery := `
        INSERT INTO transactions (hash, from_address, to_address, amount, token, timestamp, type)
        VALUES ($1, $2, $3, $4, $5, $6, $7)
        ON CONFLICT (hash, from_address, to_address, token) 
        DO UPDATE SET type = EXCLUDED.type
    `
	if _, err := tx.ExecContext(ctx, txQuery, hash, from, to, amount, token, timestamp, txType); err != nil {
		return fmt.Errorf("寫入交易紀錄失敗: %w", err)
	}

	// 3. 全部成功，提交交易
	return tx.Commit()
}

// =====================================================================
// [Method] ResolveLabel: 情報實體解析
// =====================================================================
func (r *txRepository) ResolveLabel(ctx context.Context, address string) string {
	var label string
	err := r.db.QueryRowContext(ctx, "SELECT label FROM wallets WHERE address = $1", address).Scan(&label)
	if err != nil {
		// 查無資料時，優雅降級回傳預設值
		return "wallet"
	}
	return label
}



// =====================================================================
// [Method] GetGraph: 核心圖論引擎 (Graph Engine)
// Design Decision: 捨棄 ORM，採用原生 Raw SQL 與 Recursive CTE。
// Why: ORM (如 GORM) 無法有效編譯複雜的遞迴查詢。為了壓榨 PostgreSQL 
//      的圖論遍歷效能，直接編寫最佳化的 CTE 是唯一且最專業的解法。
// =====================================================================
func (r *txRepository) GetGraph(ctx context.Context, input string, isTxHash bool, startTime, endTime int64) ([]domain.CytoElement, error) {
	var query string
	var args []interface{}

	if isTxHash {
		// FLOW 模式 (以單一 Tx 追蹤，通常不需時間限制，因為本身就是從該時序出發)
		query = `
            WITH RECURSIVE trace_path AS (
                SELECT hash, from_address, to_address, amount, timestamp, token, type
                FROM transactions 
                WHERE hash = $1 AND type = 'Trace'
                
                UNION
                
                SELECT t.hash, t.from_address, t.to_address, t.amount, t.timestamp, t.token, t.type
                FROM transactions t
                JOIN trace_path p ON t.from_address = p.to_address
                WHERE t.type = 'Trace' AND t.timestamp >= p.timestamp
            )
            SELECT p.hash, p.timestamp, p.from_address, w1.label AS from_label,
                   p.to_address, w2.label AS to_label, p.amount, p.token, p.type
            FROM trace_path p
            JOIN wallets w1 ON p.from_address = w1.address
            JOIN wallets w2 ON p.to_address = w2.address
            ORDER BY p.timestamp ASC 
            LIMIT 100;
        `
		args = []interface{}{input}
	} else {
		// BROAD 模式 (N-Degree 拓撲發散，套用嚴格時間窗)
		startAddress := input
		query = `
            WITH RECURSIVE connected_nodes AS (
                SELECT $1::varchar AS address, 0 AS depth
                UNION
                SELECT 
                    CASE WHEN t.from_address = c.address THEN t.to_address ELSE t.from_address END, 
                    c.depth + 1
                FROM transactions t 
                JOIN connected_nodes c ON (t.from_address = c.address OR t.to_address = c.address)
                JOIN wallets w ON c.address = w.address
                WHERE c.depth < 3 AND (w.label IN ('wallet', 'HighRisk') OR c.depth = 0)
                -- 💡 時間邊界約束：過濾掉不在期間內的舊資料
                AND ($2::bigint = 0 OR t.timestamp >= $2)
                AND ($3::bigint = 0 OR t.timestamp <= $3)
            ),
            min_depth_nodes AS (
                SELECT address, MIN(depth) as depth FROM connected_nodes GROUP BY address
            )
            SELECT t.hash, t.timestamp, t.from_address, w1.label AS from_label,
                t.to_address, w2.label AS to_label, t.amount, t.token, t.type
            FROM transactions t
            JOIN min_depth_nodes n1 ON t.from_address = n1.address
            JOIN min_depth_nodes n2 ON t.to_address = n2.address
            JOIN wallets w1 ON t.from_address = w1.address
            JOIN wallets w2 ON t.to_address = w2.address
            GROUP BY t.hash, t.timestamp, t.from_address, w1.label, t.to_address, w2.label, t.amount, t.token, t.type, n1.depth, n2.depth
            ORDER BY LEAST(n1.depth, n2.depth) ASC, t.timestamp DESC
            LIMIT 250; 
        `
		args = []interface{}{startAddress, startTime, endTime}
	}

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("執行圖論查詢失敗: %w", err)
	}
	defer rows.Close()

    // ... (以下資料轉型層代碼完全不變，維持原樣) ...
	var elements []domain.CytoElement
	addedNodes := make(map[string]bool)

	for rows.Next() {
		var hash, fromAddr, fromLabel, toAddr, toLabel, token, txType string
		var amount float64
		var timestamp int64

		if err := rows.Scan(&hash, &timestamp, &fromAddr, &fromLabel, &toAddr, &toLabel, &amount, &token, &txType); err != nil {
			continue
		}

		if !addedNodes[fromAddr] {
			displayLabel := fromAddr
			if len(fromAddr) >= 10 {
				displayLabel = fromAddr[:6] + "..." + fromAddr[len(fromAddr)-4:]
			}
			if fromLabel != "wallet" && fromLabel != "HighRisk" && fromLabel != "Mixer" {
				displayLabel = fromLabel
			}

			elements = append(elements, domain.CytoElement{Data: domain.CytoData{
				ID: fromAddr, Label: displayLabel, Type: fromLabel,
				IsTarget: (!isTxHash && fromAddr == input) || (isTxHash && hash == input),
			}})
			addedNodes[fromAddr] = true
		}

		if !addedNodes[toAddr] {
			displayLabel := toAddr
			if len(toAddr) >= 10 {
				displayLabel = toAddr[:6] + "..." + toAddr[len(toAddr)-4:]
			}
			if toLabel != "wallet" && toLabel != "HighRisk" && toLabel != "Mixer" {
				displayLabel = toLabel
			}

			elements = append(elements, domain.CytoElement{Data: domain.CytoData{
				ID: toAddr, Label: displayLabel, Type: toLabel,
				IsTarget: !isTxHash && toAddr == input,
			}})
			addedNodes[toAddr] = true
		}

		timeStr := time.Unix(timestamp, 0).Format("01/02 15:04")
		formattedAmount := fmt.Sprintf("%.2f %s", amount, token)
		edgeLabel := fmt.Sprintf("%s\n🕒 %s", formattedAmount, timeStr)

		elements = append(elements, domain.CytoElement{
			Data: domain.CytoData{
				ID: hash, Source: fromAddr, Target: toAddr, Amount: formattedAmount,
				Time: timeStr, EdgeLabel: edgeLabel, Type: txType,
			},
		})
	}
	return elements, nil
}