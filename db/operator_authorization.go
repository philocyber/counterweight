package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/publicsuffix"
)

type OperatorInstruction struct {
	Source string `json:"source"`
	Text   string `json:"text"`
}
type OperatorTarget struct {
	AssetID int64  `json:"asset_id"`
	Type    string `json:"type"`
	Domain  string `json:"domain,omitempty"`
	URL     string `json:"url,omitempty"`
	IP      string `json:"ip,omitempty"`
}
type OperatorAuthorization struct {
	Instructions []OperatorInstruction `json:"instructions"`
	Targets      []OperatorTarget      `json:"targets"`
	Scope        []TaskScope           `json:"scope"`
}

func (a OperatorAuthorization) Approved() bool {
	if len(a.Scope) > 0 || len(a.Targets) > 0 {
		return true
	}
	for _, instruction := range a.Instructions {
		if instruction.Source == "task" || instruction.Source == "user_goal" {
			return true
		}
	}
	return false
}

func (d *DB) TaskOperatorAuthorization(taskID int64) (OperatorAuthorization, error) {
	a := OperatorAuthorization{Instructions: []OperatorInstruction{}, Scope: []TaskScope{}, Targets: []OperatorTarget{}}
	var raw []byte
	if err := d.QueryRow(`SELECT operator_instructions FROM tasks WHERE id=$1 AND deleted_at IS NULL`, taskID).Scan(&raw); err != nil {
		return a, err
	}
	if err := json.Unmarshal(raw, &a.Instructions); err != nil {
		return a, err
	}
	scopes, err := d.Assets().ListTaskScope(taskID)
	if err != nil {
		return a, err
	}
	for _, s := range scopes {
		if s.OperatorAuthorized {
			a.Scope = append(a.Scope, s)
		}
	}
	rows, err := d.Query(`SELECT a.id,a.type,COALESCE(a.domain,''),COALESCE(a.url,''),COALESCE(a.ip::text,'') FROM task_asset_links l JOIN assets a ON a.id=l.asset_id WHERE l.task_id=$1 AND l.source='manual' ORDER BY a.id`, taskID)
	if err != nil {
		return a, err
	}
	defer rows.Close()
	for rows.Next() {
		var target OperatorTarget
		if err = rows.Scan(&target.AssetID, &target.Type, &target.Domain, &target.URL, &target.IP); err != nil {
			return a, err
		}
		a.Targets = append(a.Targets, target)
	}
	return a, rows.Err()
}
func (s *ExplorationStore) TaskOperatorAuthorization(taskID int64) (OperatorAuthorization, error) {
	return s.db.TaskOperatorAuthorization(taskID)
}

func (s *ExplorationStore) TaskGoal(taskID int64) (string, error) {
	if s == nil || s.db == nil {
		return "", nil
	}
	var goal string
	err := s.db.QueryRow(`SELECT COALESCE(goal,'') FROM tasks WHERE id=$1 AND deleted_at IS NULL`, taskID).Scan(&goal)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return goal, err
}

// RecordTaskOperatorInstruction is called only by authenticated human handlers.
// Agent-proposed scope and generated child-task text never enter this history.
func (d *DB) RecordTaskOperatorInstruction(taskID int64, source, text string) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = d.recordTaskOperatorInstruction(tx, taskID, source, text); err != nil {
		return err
	}
	return tx.Commit()
}
func (d *DB) recordTaskOperatorInstruction(tx *sql.Tx, taskID int64, source, text string) error {
	if source != "task" && source != "user_message" && source != "worker_message" && source != "user_goal" {
		return fmt.Errorf("invalid operator instruction source")
	}
	text = RedactChatMentionLabels(text)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	raw, err := json.Marshal([]OperatorInstruction{{Source: source, Text: text}})
	if err != nil {
		return err
	}
	// Repeated HTTP submissions do not restore a subsequently revoked scope.
	result, err := tx.Exec(`UPDATE tasks SET operator_instructions=operator_instructions||$2::jsonb WHERE id=$1 AND deleted_at IS NULL AND NOT operator_instructions @> $2::jsonb`, taskID, raw)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil || n == 0 {
		return err
	}
	assets := d.Assets()
	assets.tx = tx
	for _, target := range operatorPromptTargets(text) {
		target.TaskID = taskID
		target.Source = "manual"
		target.Reason = "Authorized by user via " + source
		if err = assets.upsertTaskScope(target); err != nil {
			return err
		}
	}
	return nil
}

var operatorTargetToken = regexp.MustCompile("https?://[^\\s<>\"'`]+|(?:\\*\\.)?(?:[a-zA-Z0-9-]+\\.)+[a-zA-Z]{2,}\\b|(?:[0-9]{1,3}\\.){3}[0-9]{1,3}(?:/[0-9]{1,2})?|\\[[0-9a-fA-F:]+\\](?:/[0-9]{1,3})?")
var operatorReference = regexp.MustCompile(`(?i)^\s*(?:references?|documentation|docs|examples?|observed|response|payload|referencia|documentación|respuesta|ejemplo)\s*:`)
var operatorMarkdownLink = regexp.MustCompile(`\[[^\]\n]*\]\([^\)\n]*\)`)
var operatorClause = regexp.MustCompile(`[\n;]|\.\s+`)
var operatorExclusion = regexp.MustCompile(`(?i)\b(?:exclude\w*|except\w*|never|outside|avoid|not|no|fuera|excepto|exclu\w*|evita\w*)\b[\s:]`)

// Literal target identities are exact hosts unless the user explicitly supplies
// a registrable-domain wildcard or a CIDR. Never infer a company or parent domain.
// Prose that is not unambiguous stays in the original instructions for the agent.
func operatorPromptTargets(text string) []TaskScope {
	// A reference token carries the label of a stored record, not operator prose.
	text = ChatMentionPattern.ReplaceAllString(text, " ")
	// Quoted observations, code samples, and reference links are not requests to
	// test those identities. Keep their full text in the original request only.
	var direct strings.Builder
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || strings.HasPrefix(trimmed, ">") || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "<") || operatorReference.MatchString(trimmed) {
			continue
		}
		direct.WriteString(operatorMarkdownLink.ReplaceAllString(line, ""))
		direct.WriteByte('\n')
	}
	text = direct.String()

	out := []TaskScope{}
	seen := map[string]bool{}
	excluded := map[string]bool{}
	for _, clause := range operatorClause.Split(text, -1) {
		boundary := len(clause)
		if at := operatorExclusion.FindStringIndex(clause); at != nil {
			boundary = at[0]
		}
		for _, loc := range operatorTargetToken.FindAllStringIndex(clause, -1) {
			token := strings.TrimRight(clause[loc[0]:loc[1]], ".,)]}")
			if loc[0] > 0 && (clause[loc[0]-1] == '@' || clause[loc[0]-1] == '/' || clause[loc[0]-1] == '.') {
				continue
			}
			wildcard := strings.HasPrefix(token, "*.")
			host := strings.TrimPrefix(token, "*.")
			isURL := strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://")
			if isURL {
				u, err := url.Parse(host)
				if err != nil || u.User != nil {
					continue
				}
				host = u.Hostname()
			}
			host = strings.ToLower(strings.TrimSuffix(host, "."))
			target := TaskScope{Kind: "subdomain", Domain: host}
			if _, network, err := net.ParseCIDR(host); err == nil {
				target = TaskScope{Kind: "cidr", Net: network.String()}
			} else if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil {
				target = TaskScope{Kind: "ip", Net: ipToHostCIDR(ip.String())}
			} else {
				suffix, icann := publicsuffix.PublicSuffix(host)
				if !isURL && strings.Contains("|md|txt|json|csv|pdf|html|js|ts|py|go|zip|log|sh|docx|", "|"+suffix+"|") {
					continue
				}
				if !isURL && !icann && suffix != "test" && suffix != "invalid" && suffix != "localhost" {
					continue
				}
				if wildcard {
					root, _ := RootDomain(host)
					if root != host {
						continue
					}
					target.Kind = "root_domain"
				}
			}
			key := target.Kind + ":" + target.Domain + target.Net
			if loc[0] >= boundary {
				excluded[key] = true
				continue
			}
			if !seen[key] {
				seen[key] = true
				out = append(out, target)
			}
		}
	}
	filtered := out[:0]
	for _, target := range out {
		if !excluded[target.Kind+":"+target.Domain+target.Net] {
			filtered = append(filtered, target)
		}
	}
	return filtered
}
