package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"ai-stock-service/internal/models"
	"ai-stock-service/internal/repository"
)

// rankListHandler serves rank list data (e.g. pennant setups) via the API.
type rankListHandler struct {
	rankListRepo *repository.RankListRepo
}

// getRankListByType returns all rows for a given list type and date.
// GET /api/v1/rank-lists/{listType}/{date}
func (h *rankListHandler) getRankListByType(w http.ResponseWriter, r *http.Request) {
	listTypeStr := r.PathValue("listType")
	dateStr := r.PathValue("date")

	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid date — expected YYYY-MM-DD")
		return
	}

	lt := models.ListType(listTypeStr)
	ranks, err := h.rankListRepo.GetRankList(r.Context(), date, lt)
	if err != nil {
		slog.Error("get rank list", "listType", listTypeStr, "date", dateStr, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load rank list")
		return
	}

	if len(ranks) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{
			"list_type": listTypeStr,
			"date":      dateStr,
			"ranks":     []any{},
		})
		return
	}

	type rankEntry struct {
		Rank   int            `json:"rank"`
		Ticker string         `json:"ticker"`
		Score  float64        `json:"score"`
		Reason map[string]any `json:"reason,omitempty"`
	}

	out := make([]rankEntry, 0, len(ranks))
	for i := range ranks {
		r := &ranks[i]
		var reason map[string]any
		if len(r.Reason) > 0 {
			_ = json.Unmarshal(r.Reason, &reason)
		}
		out = append(out, rankEntry{
			Rank:   r.Rank,
			Ticker: r.Ticker,
			Score:  r.Score,
			Reason: reason,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"list_type": listTypeStr,
		"date":      dateStr,
		"ranks":     out,
	})
}
