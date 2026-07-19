package repository

import (
	"context"
	"fmt"
	"time"

	"ai-stock-service/internal/services/ranking"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ── EPWeights ──────────────────────────────────────────────────────────────────

// EPWeights holds one row from the ep_score_weights table.
type EPWeights struct {
	ID                   int64     `db:"id"`
	Version              int       `db:"version"`
	Active               bool      `db:"active"`
	EventQualityScore    float64   `db:"event_quality_score"`
	VolumeSpikeScore     float64   `db:"volume_spike_score"`
	FollowThroughScore   float64   `db:"follow_through_score"`
	TrendAlignmentScore  float64   `db:"trend_alignment_score"`
	EarningsQualityScore float64   `db:"earnings_quality_score"`
	OptionsFlowScore     float64   `db:"options_flow_score"`
	FloatRotationScore   float64   `db:"float_rotation_score"`
	RegimeMultiplier     float64   `db:"regime_multiplier"`
	SectorMultiplier     float64   `db:"sector_multiplier"`
	TrainingSamples      int       `db:"training_samples"`
	TestAUC              float64   `db:"test_auc"`
	CreatedAt            time.Time `db:"created_at"`
}

// ── MomentumWeights ────────────────────────────────────────────────────────────

// MomentumWeights holds one row from the momentum_score_weights table.
type MomentumWeights struct {
	ID                      int64     `db:"id"`
	Version                 int       `db:"version"`
	Active                  bool      `db:"active"`
	BreakoutStrength        float64   `db:"breakout_strength"`
	RelativeStrength        float64   `db:"relative_strength"`
	VolumeExpansion         float64   `db:"volume_expansion"`
	VolumePriceConfirmation float64   `db:"volume_price_confirmation"`
	TrendConsistency        float64   `db:"trend_consistency"`
	RegimeMultiplier        float64   `db:"regime_multiplier"`
	SectorMultiplier        float64   `db:"sector_multiplier"`
	TrainingSamples         int       `db:"training_samples"`
	TestAUC                 float64   `db:"test_auc"`
	CreatedAt               time.Time `db:"created_at"`
}

// MomentumWeightsRepo handles persistence for the momentum_score_weights table.
type MomentumWeightsRepo struct {
	db dbPool
}

// NewMomentumWeightsRepo creates a new MomentumWeightsRepo backed by a live pool.
func NewMomentumWeightsRepo(db *pgxpool.Pool) *MomentumWeightsRepo {
	return &MomentumWeightsRepo{db: db}
}

// GetActive returns the currently active Momentum weight set.
func (r *MomentumWeightsRepo) GetActive(ctx context.Context) (*MomentumWeights, error) {
	var m MomentumWeights
	err := r.db.QueryRow(ctx, `
		SELECT id, version, active,
		       breakout_strength, relative_strength, volume_expansion,
		       volume_price_confirmation, trend_consistency,
		       regime_multiplier, sector_multiplier,
		       training_samples, test_auc, created_at
		FROM   momentum_score_weights
		WHERE  active = TRUE
		LIMIT  1
	`).Scan(
		&m.ID, &m.Version, &m.Active,
		&m.BreakoutStrength, &m.RelativeStrength, &m.VolumeExpansion,
		&m.VolumePriceConfirmation, &m.TrendConsistency,
		&m.RegimeMultiplier, &m.SectorMultiplier,
		&m.TrainingSamples, &m.TestAUC, &m.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("MomentumWeightsRepo.GetActive: %w", err)
	}
	return &m, nil
}

// DeactivateAll sets active=FALSE on every row.
func (r *MomentumWeightsRepo) DeactivateAll(ctx context.Context) error {
	_, err := r.db.Exec(ctx, `UPDATE momentum_score_weights SET active = FALSE`)
	if err != nil {
		return fmt.Errorf("MomentumWeightsRepo.DeactivateAll: %w", err)
	}
	return nil
}

// GetActiveWeights returns the active weights as a MomentumWeights struct.
func (r *MomentumWeightsRepo) GetActiveWeights(ctx context.Context) (ranking.MomentumWeights, error) {
	w, err := r.GetActive(ctx)
	if err != nil {
		return ranking.MomentumWeights{}, err
	}
	return ranking.MomentumWeights{
		BreakoutStrength:        w.BreakoutStrength,
		RelativeStrength:        w.RelativeStrength,
		VolumeExpansion:         w.VolumeExpansion,
		VolumePriceConfirmation: w.VolumePriceConfirmation,
		TrendConsistency:        w.TrendConsistency,
		RegimeMult:              w.RegimeMultiplier,
		SectorMult:              w.SectorMultiplier,
	}, nil
}

// Insert creates a new weight row with the given version.
func (r *MomentumWeightsRepo) Insert(ctx context.Context, w *MomentumWeights) error {

	_, err := r.db.Exec(ctx, `
		INSERT INTO momentum_score_weights (
			version, active,
			breakout_strength, relative_strength, volume_expansion,
			volume_price_confirmation, trend_consistency,
			regime_multiplier, sector_multiplier,
			training_samples, test_auc
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
	`,
		w.Version, w.Active,
		w.BreakoutStrength, w.RelativeStrength, w.VolumeExpansion,
		w.VolumePriceConfirmation, w.TrendConsistency,
		w.RegimeMultiplier, w.SectorMultiplier,
		w.TrainingSamples, w.TestAUC,
	)
	if err != nil {
		return fmt.Errorf("MomentumWeightsRepo.Insert: %w", err)
	}
	return nil
}
