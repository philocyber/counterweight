package db

import "github.com/Philocyber/counterweight/execution"

// AppendContainerExecutionSegment snapshots the immutable image config digest
// and credentials capability alongside the existing, archive-carried columns.
func (d *DB) AppendContainerExecutionSegment(taskID int64, mode, version, digest string) error {
	c := execution.Caps(mode)
	_, err := d.Exec(`INSERT INTO task_execution_segments(task_id,mode,files_confined,processes_confined,network_confined,app_version,image_digest,credentials_isolated) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, taskID, mode, c.FilesConfined, c.ProcessesConfined, c.NetworkConfined, version, digest, c.CredentialsIsolated)
	return err
}
func (s *ExplorationStore) ExecutionImageDigest(taskID int64) (string, error) {
	var digest string
	err := s.db.QueryRow(`SELECT COALESCE(image_digest,'') FROM task_execution_segments WHERE task_id=$1 ORDER BY started_at DESC,id DESC LIMIT 1`, taskID).Scan(&digest)
	return digest, err
}
