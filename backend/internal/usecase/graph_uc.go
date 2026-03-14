package usecase

import (
	"context"
	"backend/internal/domain"
)

type graphUsecase struct {
	BaseUsecase
}

func NewGraphUsecase(base BaseUsecase) domain.GraphUsecase {
	return &graphUsecase{BaseUsecase: base}
}

func (uc *graphUsecase) GetGraph(ctx context.Context, queryIdentifier string, startTime, endTime int64) ([]domain.CytoElement, error) {
	isTxHash := len(queryIdentifier) == 66
	return uc.TxRepo.GetGraph(ctx, queryIdentifier, isTxHash, startTime, endTime)
}