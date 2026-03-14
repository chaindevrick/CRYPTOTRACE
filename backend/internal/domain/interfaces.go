package domain

import "context"

type TransactionRepository interface {
	UpsertTx(ctx context.Context, from, to, hash, token, txType string, amount float64, timestamp int64) error
	GetGraph(ctx context.Context, rootAddress string, isTxHash bool, startTime, endTime int64) ([]CytoElement, error)
	ResolveLabel(ctx context.Context, address string) string
}

type EtherscanRepository interface {
	GetTokenTxs(ctx context.Context, address string, sort string) ([]EtherscanTx, error)
	GetTxSender(ctx context.Context, txHash string) (string, error)
	GetContractName(ctx context.Context, address string) (string, error)
}

type AIRepository interface {
	TriggerAnalysis(ctx context.Context, address string, startTime, endTime int64) error
}

type AnalyzerUsecase interface {
	Analyze(ctx context.Context, address string, startTime, endTime int64) (int, error)
}

type TracerUsecase interface {
	Trace(ctx context.Context, input string) error
}

type GraphUsecase interface {
	GetGraph(ctx context.Context, address string, startTime, endTime int64) ([]CytoElement, error)
}