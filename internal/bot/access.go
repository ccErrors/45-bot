package bot

import "github.com/ccErrors/race-bot/internal/storage"

// canView is the single access check for opening a race:
// the owner always, anyone else only if the race is public.
func canView(r *storage.Race, userID int64) bool {
	return r.UserID == userID || r.Public
}

// canEdit guards race settings: owner only.
func canEdit(r *storage.Race, userID int64) bool {
	return r.UserID == userID
}

// canUseAggregation guards viewing and editing aggregations: owner only for now.
// An aggregation may contain private races, so making it public needs its own rules.
func canUseAggregation(a *storage.Aggregation, userID int64) bool {
	return a.UserID == userID
}
