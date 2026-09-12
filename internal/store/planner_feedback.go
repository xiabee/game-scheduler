package store

import "errors"

// RecommendationFeedback is the execution-outcome rollup for one
// recommendation's linked task (NC7 second slice). It reports observable
// facts only: how the linked task's executions went. It deliberately
// excludes any material accounting — one successful run does not prove a
// material was gained, so owned_count and the recommendation lifecycle are
// never touched by this path; those stay manual decisions.

// RecommendationFeedback aggregates the executions of a recommendation's
// task. Counts cover the retained execution history (the retention window
// prunes old rows, so the numbers describe what is still observable).
type RecommendationFeedback struct {
	RecommendationID int64  `json:"recommendation_id"`
	TaskID           *int64 `json:"task_id,omitempty"`
	// TaskMissing reports a dangling link: the recommendation points at a
	// task row that no longer exists. All counts are zero in that state.
	TaskMissing bool `json:"task_missing,omitempty"`

	TotalExecutions  int `json:"total_executions"`
	SuccessRuns      int `json:"success_runs"`
	FailedRuns       int `json:"failed_runs"`
	CancelledRuns    int `json:"cancelled_runs"`
	PendingOrRunning int `json:"pending_or_running"`

	// LastExecution is the newest retained execution of the task, if any.
	LastExecution *ExecutionMeta `json:"last_execution,omitempty"`

	// EstimatedRuns is the recommendation's own plan figure, carried here so
	// clients can compare planned vs observed without a second call.
	EstimatedRuns int `json:"estimated_runs"`
}

// RecommendationFeedback builds the rollup for one recommendation. Unknown
// recommendations are ErrNotFound.
func (s *Store) RecommendationFeedback(recID int64) (RecommendationFeedback, error) {
	rec, err := s.GetFarmingRecommendation(recID)
	if err != nil {
		return RecommendationFeedback{}, err
	}
	fb := RecommendationFeedback{RecommendationID: rec.ID, EstimatedRuns: rec.EstimatedRuns}
	if rec.TaskID == nil {
		return fb, nil
	}
	fb.TaskID = rec.TaskID
	if _, err := s.GetTask(*rec.TaskID); err != nil {
		if errors.Is(err, ErrNotFound) {
			fb.TaskMissing = true
			return fb, nil
		}
		return RecommendationFeedback{}, err
	}
	taskID := *rec.TaskID
	err = s.db.QueryRow(`SELECT COUNT(*),
			COALESCE(SUM(CASE WHEN status=? THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status=? THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status=? THEN 1 ELSE 0 END),0),
			COALESCE(SUM(CASE WHEN status IN (?,?) THEN 1 ELSE 0 END),0)
		FROM executions WHERE task_id=?`,
		StatusSuccess, StatusFailed, StatusCancelled, StatusPending, StatusRunning, taskID).
		Scan(&fb.TotalExecutions, &fb.SuccessRuns, &fb.FailedRuns, &fb.CancelledRuns, &fb.PendingOrRunning)
	if err != nil {
		return RecommendationFeedback{}, err
	}
	last, err := s.ListExecutionMetas(ExecutionFilter{TaskID: taskID, Limit: 1})
	if err != nil {
		return RecommendationFeedback{}, err
	}
	if len(last) == 1 {
		fb.LastExecution = &last[0]
	}
	return fb, nil
}
