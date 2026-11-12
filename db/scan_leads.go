package db

import (
	"database/sql"
	"strings"
	"time"
)

// Scan run statuses (scan_runs.status).
const (
	ScanRunning     = "running"
	ScanOK          = "ok"
	ScanUnavailable = "unavailable"
	ScanFailed      = "failed"
)

// Scan lead states (scan_leads.state). A lead is delivered to the review at most
// once and becomes confirmed or dismissed only through a finding or an explicit
// resolution; it is never a finding by itself.
const (
	LeadNew       = "new"
	LeadDelivered = "delivered"
	LeadConfirmed = "confirmed"
	LeadDismissed = "dismissed"
)

// ScanRun is one supplementary scanner's run over a task's repository.
type ScanRun struct {
	TaskID      int64      `json:"task_id"`
	Scanner     string     `json:"scanner"`
	Status      string     `json:"status"`
	Reason      string     `json:"reason,omitempty"`
	ToolVersion string     `json:"tool_version,omitempty"`
	LeadsFound  int        `json:"leads_found"`
	LeadsStored int        `json:"leads_stored"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`
}

// ScanLead is one unverified pointer from a scanner. It carries no secret value
// and no scanner severity.
type ScanLead struct {
	ID           int64      `json:"id"`
	TaskID       int64      `json:"task_id"`
	Scanner      string     `json:"scanner"`
	Kind         string     `json:"kind"` // secret | advisory
	RuleID       string     `json:"rule_id"`
	Title        string     `json:"title"`
	File         string     `json:"file,omitempty"`
	Line         int        `json:"line,omitempty"`
	Package      string     `json:"package,omitempty"`
	Version      string     `json:"version,omitempty"`
	FixedVersion string     `json:"fixed_version,omitempty"`
	Aliases      string     `json:"aliases,omitempty"` // space separated
	SeverityRank float64    `json:"-"`                 // scanner score, used only to order delivery
	Detail       string     `json:"detail,omitempty"`
	Fingerprint  string     `json:"fingerprint"`
	State        string     `json:"state"`
	FindingID    *int64     `json:"finding_id,omitempty"`
	Resolution   string     `json:"resolution,omitempty"`
	ResolvedBy   string     `json:"resolved_by,omitempty"` // agent | finding_match | existing_finding
	CreatedAt    time.Time  `json:"created_at"`
	DeliveredAt  *time.Time `json:"delivered_at,omitempty"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
}

const scanLeadCols = `id, task_id, scanner, kind, rule_id, title, file, line, package, version, fixed_version, aliases, detail,
 fingerprint, state, finding_id, resolution, resolved_by, created_at, delivered_at, resolved_at, severity_rank`

func scanLeads(rows *sql.Rows, err error) ([]*ScanLead, error) {
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ScanLead{}
	for rows.Next() {
		l := &ScanLead{}
		if err := rows.Scan(&l.ID, &l.TaskID, &l.Scanner, &l.Kind, &l.RuleID, &l.Title, &l.File, &l.Line, &l.Package, &l.Version,
			&l.FixedVersion, &l.Aliases, &l.Detail, &l.Fingerprint, &l.State, &l.FindingID, &l.Resolution, &l.ResolvedBy,
			&l.CreatedAt, &l.DeliveredAt, &l.ResolvedAt, &l.SeverityRank); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// BeginScanRun records that a scanner started for a task, replacing an earlier
// run of the same scanner.
func (d *DB) BeginScanRun(taskID int64, scanner string) error {
	_, err := d.Exec(`INSERT INTO scan_runs(task_id, scanner, status) VALUES ($1,$2,'running')
ON CONFLICT (task_id, scanner) DO UPDATE SET status='running', reason='', tool_version='', leads_found=0, leads_stored=0,
 started_at=now(), finished_at=NULL`, taskID, scanner)
	return err
}

// FinishScanRun records a scanner's outcome.
func (d *DB) FinishScanRun(taskID int64, scanner, status, reason, version string, found, stored int) error {
	_, err := d.Exec(`INSERT INTO scan_runs(task_id, scanner, status, reason, tool_version, leads_found, leads_stored, finished_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,now())
ON CONFLICT (task_id, scanner) DO UPDATE SET status=$3, reason=$4, tool_version=$5, leads_found=$6, leads_stored=$7, finished_at=now()`,
		taskID, scanner, status, reason, version, found, stored)
	return err
}

// ListScanRuns returns a task's scanner runs in scanner order.
func (d *DB) ListScanRuns(taskID int64) ([]*ScanRun, error) {
	rows, err := d.Query(`SELECT task_id, scanner, status, reason, tool_version, leads_found, leads_stored, started_at, finished_at
FROM scan_runs WHERE task_id=$1 ORDER BY scanner`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*ScanRun{}
	for rows.Next() {
		r := &ScanRun{}
		if err := rows.Scan(&r.TaskID, &r.Scanner, &r.Status, &r.Reason, &r.ToolVersion, &r.LeadsFound, &r.LeadsStored, &r.StartedAt, &r.FinishedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ScanRunsInProgress counts a task's scanner runs started after since that have
// not finished. A run older than since is treated as abandoned (a restart can
// leave a run marked running forever).
func (d *DB) ScanRunsInProgress(taskID int64, since time.Time) (int, error) {
	var n int
	err := d.QueryRow(`SELECT count(*) FROM scan_runs WHERE task_id=$1 AND status='running' AND started_at > $2`, taskID, since).Scan(&n)
	return n, err
}

// AddScanLeads stores leads, ignoring any whose fingerprint the task already
// has, and returns how many were new.
func (d *DB) AddScanLeads(taskID int64, leads []ScanLead) (int, error) {
	added := 0
	for _, l := range leads {
		res, err := d.Exec(`INSERT INTO scan_leads(task_id, scanner, kind, rule_id, title, file, line, package, version, fixed_version, aliases, detail, fingerprint, severity_rank)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) ON CONFLICT (task_id, fingerprint) DO NOTHING`,
			taskID, l.Scanner, l.Kind, l.RuleID, l.Title, l.File, l.Line, l.Package, l.Version, l.FixedVersion, l.Aliases, l.Detail, l.Fingerprint, l.SeverityRank)
		if err != nil {
			return added, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	return added, nil
}

// ListScanLeads returns every lead of a task, oldest first.
func (d *DB) ListScanLeads(taskID int64) ([]*ScanLead, error) {
	return scanLeads(d.Query(`SELECT `+scanLeadCols+` FROM scan_leads WHERE task_id=$1 ORDER BY id`, taskID))
}

// ListScanLeadsByState returns a task's leads in one state, oldest first.
func (d *DB) ListScanLeadsByState(taskID int64, state string) ([]*ScanLead, error) {
	return scanLeads(d.Query(`SELECT `+scanLeadCols+` FROM scan_leads WHERE task_id=$1 AND state=$2 ORDER BY id`, taskID, state))
}

// GetScanLead returns one lead of a task, or nil.
func (d *DB) GetScanLead(taskID, leadID int64) (*ScanLead, error) {
	ls, err := scanLeads(d.Query(`SELECT `+scanLeadCols+` FROM scan_leads WHERE task_id=$1 AND id=$2`, taskID, leadID))
	if err != nil || len(ls) == 0 {
		return nil, err
	}
	return ls[0], nil
}

// CountScanLeadsDelivered counts the leads ever handed to a task's review,
// whatever their present state.
func (d *DB) CountScanLeadsDelivered(taskID int64) (int, error) {
	var n int
	err := d.QueryRow(`SELECT count(*) FROM scan_leads WHERE task_id=$1 AND delivered_at IS NOT NULL`, taskID).Scan(&n)
	return n, err
}

// ClaimScanLeads marks the given new leads delivered and returns the ids that
// were actually claimed. The state check makes a hand-over happen once per lead
// even when two callers race.
func (d *DB) ClaimScanLeads(taskID int64, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := d.Query(`UPDATE scan_leads SET state='delivered', delivered_at=now()
WHERE task_id=$1 AND state='new' AND id = ANY($2::bigint[]) RETURNING id`, taskID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UnclaimScanLeads returns claimed leads to new when their hand-over failed.
func (d *DB) UnclaimScanLeads(taskID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := d.Exec(`UPDATE scan_leads SET state='new', delivered_at=NULL WHERE task_id=$1 AND state='delivered' AND id = ANY($2::bigint[])`, taskID, ids)
	return err
}

// ResolveScanLead records that a lead was confirmed (optionally by a finding)
// or dismissed with a reason. resolvedBy says who decided: agent, finding_match
// or existing_finding. It reports whether a lead changed.
func (d *DB) ResolveScanLead(taskID, leadID int64, state string, findingID int64, reason, resolvedBy string) (bool, error) {
	if state != LeadConfirmed && state != LeadDismissed {
		return false, nil
	}
	var fid any
	if findingID > 0 {
		fid = findingID
	}
	res, err := d.Exec(`UPDATE scan_leads SET state=$3, finding_id=$4, resolution=$5, resolved_by=$6, resolved_at=now()
WHERE task_id=$1 AND id=$2`, taskID, leadID, state, fid, strings.TrimSpace(reason), resolvedBy)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
