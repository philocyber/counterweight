package db

// ChangeReviewTaskIDs returns the live code review tasks that reviewed the
// given head commit of a repository as a change, newest first. The repository
// compares case-insensitively, as GitHub does.
func (d *DB) ChangeReviewTaskIDs(repository, headCommit string) ([]int64, error) {
	rows, err := d.Query(`SELECT target.task_id FROM task_code_targets target JOIN tasks ON tasks.id=target.task_id
WHERE lower(target.repository)=lower($1) AND target.change->>'head_commit'=$2 AND tasks.deleted_at IS NULL
ORDER BY target.task_id DESC`, repository, headCommit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
