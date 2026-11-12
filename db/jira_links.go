package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// JiraLink records a Jira issue filed from Counterweight: the epic of a task or
// the issue of a finding, on one Jira site.
type JiraLink struct {
	ID             int64      `json:"id"`
	Kind           string     `json:"kind"` // epic or finding
	TaskID         *int64     `json:"task_id,omitempty"`
	FindingID      *int64     `json:"finding_id,omitempty"`
	Site           string     `json:"site"`
	ProjectKey     string     `json:"project_key"`
	IssueKey       string     `json:"issue_key"`
	IssueID        string     `json:"issue_id"`
	IssueTypeID    string     `json:"issue_type_id"`     // epics: the issue type of the task's findings
	SecurityLevel  string     `json:"security_level_id"` // epics: the security level of the task's issues
	PendingMove    string     `json:"-"`                 // comma-separated status categories still to reach
	LastState      string     `json:"-"`                 // finding state last reported to Jira
	StatusName     string     `json:"status_name"`
	StatusCategory string     `json:"status_category"`
	CreatedAt      time.Time  `json:"created_at"`
	SyncedAt       *time.Time `json:"synced_at,omitempty"`
}

const jiraLinkCols = `id, kind, task_id, finding_id, site, project_key, issue_key, issue_id, issue_type_id, security_level_id, pending_move, last_state, status_name, status_category, created_at, synced_at`

func scanJiraLink(row interface{ Scan(...any) error }) (*JiraLink, error) {
	l := &JiraLink{}
	err := row.Scan(&l.ID, &l.Kind, &l.TaskID, &l.FindingID, &l.Site, &l.ProjectKey, &l.IssueKey, &l.IssueID, &l.IssueTypeID, &l.SecurityLevel, &l.PendingMove,
		&l.LastState, &l.StatusName, &l.StatusCategory, &l.CreatedAt, &l.SyncedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return l, err
}

func (d *DB) listJiraLinks(ctx context.Context, where string, args ...any) ([]*JiraLink, error) {
	rows, err := d.QueryContext(ctx, `SELECT `+jiraLinkCols+` FROM jira_links WHERE `+where+` ORDER BY id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*JiraLink{}
	for rows.Next() {
		l, err := scanJiraLink(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// JiraEpicLink is the epic filed for a task on a site, or nil.
func (d *DB) JiraEpicLink(ctx context.Context, taskID int64, site string) (*JiraLink, error) {
	return scanJiraLink(d.QueryRowContext(ctx, `SELECT `+jiraLinkCols+` FROM jira_links WHERE kind='epic' AND task_id=$1 AND site=$2`, taskID, site))
}

// JiraFindingLink is the issue filed for a finding on a site, or nil.
func (d *DB) JiraFindingLink(ctx context.Context, findingID int64, site string) (*JiraLink, error) {
	return scanJiraLink(d.QueryRowContext(ctx, `SELECT `+jiraLinkCols+` FROM jira_links WHERE kind='finding' AND finding_id=$1 AND site=$2`, findingID, site))
}

// JiraTaskLinks lists the epic and finding issues of a task on a site.
func (d *DB) JiraTaskLinks(ctx context.Context, taskID int64, site string) ([]*JiraLink, error) {
	return d.listJiraLinks(ctx, `task_id=$1 AND site=$2`, taskID, site)
}

// JiraFindingLinks lists every finding issue on a site, for outbound sync.
func (d *DB) JiraFindingLinks(ctx context.Context, site string) ([]*JiraLink, error) {
	return d.listJiraLinks(ctx, `kind='finding' AND site=$1`, site)
}

// InsertJiraLink records a created issue. A concurrent insert for the same
// epic or finding wins; the existing row is returned with created=false.
func (d *DB) InsertJiraLink(ctx context.Context, l *JiraLink) (*JiraLink, bool, error) {
	row := d.QueryRowContext(ctx, `INSERT INTO jira_links(kind, task_id, finding_id, site, project_key, issue_key, issue_id, issue_type_id, security_level_id, last_state)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) ON CONFLICT DO NOTHING RETURNING `+jiraLinkCols,
		l.Kind, l.TaskID, l.FindingID, l.Site, l.ProjectKey, l.IssueKey, l.IssueID, l.IssueTypeID, l.SecurityLevel, l.LastState)
	out, err := scanJiraLink(row)
	if err != nil || out != nil {
		return out, out != nil, err
	}
	if l.Kind == "epic" && l.TaskID != nil {
		out, err = d.JiraEpicLink(ctx, *l.TaskID, l.Site)
	} else if l.FindingID != nil {
		out, err = d.JiraFindingLink(ctx, *l.FindingID, l.Site)
	}
	return out, false, err
}

// DeleteJiraLink forgets an issue, for example after it was deleted in Jira.
func (d *DB) DeleteJiraLink(ctx context.Context, id int64) error {
	_, err := d.ExecContext(ctx, `DELETE FROM jira_links WHERE id=$1`, id)
	return err
}

// SetJiraLinkState records the finding state reported to Jira and any status
// move still pending.
func (d *DB) SetJiraLinkState(ctx context.Context, id int64, state, pendingMove string) error {
	_, err := d.ExecContext(ctx, `UPDATE jira_links SET last_state=$2, pending_move=$3, synced_at=now() WHERE id=$1`, id, state, pendingMove)
	return err
}

// SetJiraLinkStatus caches the issue's workflow status as last read from Jira.
func (d *DB) SetJiraLinkStatus(ctx context.Context, id int64, name, category string) error {
	_, err := d.ExecContext(ctx, `UPDATE jira_links SET status_name=$2, status_category=$3, synced_at=now() WHERE id=$1`, id, name, category)
	return err
}
