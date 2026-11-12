package server

import (
	"strconv"

	"github.com/Philocyber/counterweight/db"
)

// markFindingsInChange sets InChange on the DTOs of a task's findings (out[i]
// belongs to fs[i]) when the task is a change-scoped code review. For any other
// task the field stays unset and is omitted from the response.
func (s *Server) markFindingsInChange(taskID string, fs []*db.DBFinding, out []FindingDTO) {
	id, err := strconv.ParseInt(taskID, 10, 64)
	if err != nil || id < 1 || len(fs) != len(out) {
		return
	}
	target, err := s.m.pg.GetTaskCodeTarget(id)
	if err != nil || target == nil || target.Change == nil {
		return
	}
	inChange := changeClassifier(target.Change, target.LocalPath)
	for i, f := range fs {
		v := inChange(f)
		out[i].InChange = &v
	}
}
