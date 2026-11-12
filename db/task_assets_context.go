package db

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// directTaskContextCTE is the current task plus exactly its explicitly related
// source tasks. It is deliberately non-recursive.
//
// Anchored assets join that set only when one of those tasks owns them.
// A hint or note that cites another task's asset must not pull that asset,
// or its parent domain, into this task's coverage graph.
const directTaskContextCTE = `
context_tasks AS (
  SELECT t.id AS task_id, t.exploration_id
  FROM tasks t
  WHERE t.id=$1 AND t.deleted_at IS NULL
  UNION ALL
  SELECT source.id, source.exploration_id
  FROM task_relations relation
  JOIN tasks source ON source.id=relation.source_task_id AND source.deleted_at IS NULL
  WHERE relation.task_id=$1
)`

const contextCoverageCTE = directTaskContextCTE + `,
target AS (
  SELECT DISTINCT a.id, a.type,
         COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, '') AS label
  FROM assets a
  JOIN task_scope ts ON (
       (ts.kind='company'     AND a.company_id = ts.company_id)
    OR (ts.kind='root_domain' AND a.root_domain = ts.domain)
    OR (ts.kind='subdomain'   AND a.domain = ts.domain)
    OR (ts.kind IN ('ip','cidr') AND ts.net >>= try_inet(a.ip))
    OR (ts.kind='icp' AND (
         lower(regexp_replace(COALESCE(a.icp,''), '[[:space:]]+', '', 'g')) = ts.value
         OR lower(regexp_replace(COALESCE(a.app_icp,''), '[[:space:]]+', '', 'g')) = ts.value
       ))
  )
  JOIN context_tasks ctx ON ctx.task_id=ts.task_id
	UNION
	SELECT a.id, a.type,
	       COALESCE(a.url, a.domain, a.ip, a.app_name, a.root_domain, '') AS label
	FROM assets a
	JOIN exploration_anchors ea ON ea.asset_id=a.id
	JOIN exploration_nodes en ON en.id=ea.node_id
	JOIN context_tasks ctx ON ctx.exploration_id=en.exploration_id
	WHERE EXISTS (SELECT 1 FROM context_tasks owner WHERE owner.task_id = ANY(a.task_ids))
),
tested AS (
  SELECT DISTINCT ea.asset_id
  FROM exploration_anchors ea
  JOIN exploration_nodes en ON en.id=ea.node_id AND en.kind='fact'
  JOIN context_tasks ctx ON ctx.exploration_id=en.exploration_id
)`

// scopeTargetCTE selects every asset that BELONGS to the current task's (and its
// direct source tasks') operator read grants — membership, not literal value: a
// root_domain scope pulls in every subdomain / service / endpoint whose own
// root_domain column equals it; an ip/cidr scope pulls in assets whose ip OR
// IP-literal host falls inside the net. $1 is the task id. Unlike contextCoverageCTE
// it carries neither the fact-anchor union nor the tested set — it is pure "in
// operator grants", independent of what has already been touched. Company/ICP
// attribution is captured when authorized, not followed as agents edit metadata.
// Explicit operator asset attachments grant only those IDs. Used by the
// agent's list_assets so a query returns the task's relevant assets, not the
// whole shared database.
const scopeTargetCTE = `
context_tasks AS (
  SELECT t.id AS task_id
  FROM tasks t
  WHERE t.id=$1 AND t.deleted_at IS NULL
  UNION ALL
  SELECT source.id
  FROM task_relations relation
  JOIN tasks source ON source.id=relation.source_task_id AND source.deleted_at IS NULL
  WHERE relation.task_id=$1
),
target AS (` + scopeGrantIDsSQL + `)`

// scopeGrantIDsSQL selects the ids of the operator-granted assets. It reads the
// context_tasks CTE and $1 (the task id) from the query it is embedded in.
const scopeGrantIDsSQL = `
  SELECT DISTINCT a.id
  FROM assets a
  JOIN task_scope ts ON ts.source='manual' AND (
       (ts.kind IN ('company','icp') AND (
          EXISTS (SELECT 1 FROM jsonb_array_elements(COALESCE(ts.read_grant->'scope','[]'::jsonb)) grant_scope WHERE
             (grant_scope->>'kind'='domain' AND
               (a.domain=grant_scope->>'domain' OR a.domain LIKE '%.' || (grant_scope->>'domain')))
             OR (grant_scope->>'kind' IN ('ip','cidr') AND
               (grant_scope->>'net')::cidr >>= try_inet(CASE
                 WHEN a.type='ip' THEN a.ip
                 WHEN a.type IN ('service','endpoint') AND (a.service_type='http' OR a.url IS NOT NULL) THEN a.domain
                 WHEN a.type='service' THEN a.ip END))
          )
       ))
    OR (ts.kind='root_domain' AND a.root_domain = ts.domain)
    OR (ts.kind='subdomain'   AND a.domain = ts.domain)
    OR (ts.kind IN ('ip','cidr') AND ts.net >>= try_inet(CASE
          WHEN a.type='ip' THEN a.ip
          WHEN a.type IN ('service','endpoint') AND (a.service_type='http' OR a.url IS NOT NULL) THEN a.domain
          WHEN a.type='service' THEN a.ip END))
  )
  JOIN context_tasks ctx ON ctx.task_id=ts.task_id
	UNION
	SELECT granted.asset_id::bigint
	FROM task_scope ts
	JOIN context_tasks ctx ON ctx.task_id=ts.task_id
	CROSS JOIN LATERAL jsonb_array_elements_text(COALESCE(ts.read_grant->'asset_ids','[]'::jsonb)) granted(asset_id)
	WHERE ts.source='manual' AND ts.kind IN ('company','icp')
	UNION
	SELECT link.asset_id
	FROM task_asset_links link
	JOIN context_tasks ctx ON ctx.task_id=link.task_id
	WHERE link.source='manual'
`

// agentVisibleAssets restricts a query to the assets an agent may learn about:
// the operator read grants (the same set list_assets uses) plus assets owned by
// the task itself or its direct sources. Coverage proposals written by agents
// select nothing outside that set. idExpr is the asset id column of the query,
// which must also define the context_tasks CTE.
func agentVisibleAssets(idExpr string) string {
	return `(EXISTS (SELECT 1 FROM assets own JOIN context_tasks owner ON owner.task_id = ANY(own.task_ids) WHERE own.id = ` + idExpr + `)
    OR ` + idExpr + ` IN (` + scopeGrantIDsSQL + `))`
}

// MaxUntestedPageSize bounds one page of the untested-assets listing.
const MaxUntestedPageSize = 100

// ListTaskScopeWithSources returns the current task's scope followed by the
// scopes of its direct source tasks. TaskScope.TaskID preserves provenance.
func (s *AssetStore) ListTaskScopeWithSources(taskID int64) ([]TaskScope, error) {
	rows, err := s.db.Query(`WITH `+directTaskContextCTE+`
SELECT ts.id, ts.task_id, ts.kind, COALESCE(ts.company_id,0), COALESCE(c.name,''), COALESCE(ts.domain,''),
       COALESCE(ts.net::text,''), COALESCE(ts.value,''), ts.source, COALESCE(ts.reason,''),
       ts.source='manual' AND ts.kind<>'keyword' AND (ts.kind NOT IN ('company','icp') OR COALESCE(ts.read_grant ? 'asset_ids',false))
FROM task_scope ts
JOIN context_tasks ctx ON ctx.task_id=ts.task_id
LEFT JOIN companies c ON c.id=ts.company_id
ORDER BY CASE WHEN ts.task_id=$1 THEN 0 ELSE 1 END, ts.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskScope{}
	for rows.Next() {
		var scope TaskScope
		var companyID int64
		if err := rows.Scan(&scope.ID, &scope.TaskID, &scope.Kind, &companyID, &scope.CompanyName, &scope.Domain, &scope.Net, &scope.Value, &scope.Source, &scope.Reason, &scope.OperatorAuthorized); err != nil {
			return nil, err
		}
		if companyID > 0 {
			scope.CompanyID = &companyID
		}
		out = append(out, scope)
	}
	return out, rows.Err()
}

// TaskCoverageWithSources computes one coverage view over the union of the
// current task and its direct sources: source scopes and anchored assets extend
// the denominator, while fact anchors count as tested. No row is copied.
func (s *AssetStore) TaskCoverageWithSources(taskID int64) (*Coverage, error) {
	return s.taskCoverageWithSources(taskID, "")
}

// TaskCoverageForAgent is TaskCoverageWithSources limited to the assets an agent
// may learn about (see agentVisibleAssets), so the counts cannot be used to
// enumerate assets outside the operator's grants.
func (s *AssetStore) TaskCoverageForAgent(taskID int64) (*Coverage, error) {
	return s.taskCoverageWithSources(taskID, " WHERE "+agentVisibleAssets("target.id"))
}

func (s *AssetStore) taskCoverageWithSources(taskID int64, filter string) (*Coverage, error) {
	cov := &Coverage{ByType: []CoverageByType{}}
	_ = s.db.QueryRow(`WITH `+directTaskContextCTE+`
SELECT count(*) FROM task_scope ts JOIN context_tasks ctx ON ctx.task_id=ts.task_id`, taskID).Scan(&cov.ScopeRows)
	rows, err := s.db.Query(`WITH `+contextCoverageCTE+`
SELECT target.type, count(*) AS total,
       count(*) FILTER (WHERE target.id IN (SELECT asset_id FROM tested)) AS tested
FROM target`+filter+` GROUP BY target.type ORDER BY target.type`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var byType CoverageByType
		if err := rows.Scan(&byType.Type, &byType.Total, &byType.Tested); err != nil {
			return nil, err
		}
		cov.ByType = append(cov.ByType, byType)
		cov.Denominator += byType.Total
		cov.Tested += byType.Tested
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if cov.Denominator > 0 {
		pct := float64(cov.Tested) / float64(cov.Denominator)
		cov.Pct = &pct
	}
	return cov, nil
}

// ListUntestedAssetsWithSources is the direct-source-aware backlog query used
// by inherited tasks. Source fact anchors remove assets from the backlog.
func (s *AssetStore) ListUntestedAssetsWithSources(taskID int64, typ string, limit, offset int) ([]CoverageAsset, int, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > MaxUntestedPageSize {
		limit = MaxUntestedPageSize
	}
	if offset < 0 {
		offset = 0
	}
	// Only agents call this listing: return nothing they could not read through
	// list_assets, whatever scope rows they proposed.
	typeFilter := " AND " + agentVisibleAssets("target.id")
	args := []any{taskID}
	if typ != "" {
		typeFilter += " AND target.type = $2"
		args = append(args, typ)
	}
	var total int
	if err := s.db.QueryRow(`WITH `+contextCoverageCTE+`
SELECT count(*) FROM target WHERE target.id NOT IN (SELECT asset_id FROM tested)`+typeFilter, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	pageArgs := append(append([]any{}, args...), limit, offset)
	limitPosition := strconv.Itoa(len(args) + 1)
	offsetPosition := strconv.Itoa(len(args) + 2)
	rows, err := s.db.Query(`WITH `+contextCoverageCTE+`
SELECT target.id, target.type, target.label FROM target
WHERE target.id NOT IN (SELECT asset_id FROM tested)`+typeFilter+`
ORDER BY target.id LIMIT $`+limitPosition+` OFFSET $`+offsetPosition, pageArgs...)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	out := []CoverageAsset{}
	for rows.Next() {
		var asset CoverageAsset
		if err := rows.Scan(&asset.ID, &asset.Type, &asset.Label); err != nil {
			return nil, total, err
		}
		out = append(out, asset)
	}
	return out, total, rows.Err()
}

// HostsByTaskWithSources resolves exact HTTP host candidates from assets that
// are attached to, anchored by, or in scope for the current task or a direct
// source. Traffic remains global and is not copied. This read helper must not be
// used for destructive task cleanup; HostsByTask intentionally retains that
// narrower, task-owned behavior.
func (s *AssetStore) HostsByTaskWithSources(taskID int64) ([]string, error) {
	return s.hostsByTaskWithSources(taskID, "")
}

// HostsByTaskForAgent is HostsByTaskWithSources limited to the assets an agent
// may learn about (see agentVisibleAssets).
func (s *AssetStore) HostsByTaskForAgent(taskID int64) ([]string, error) {
	return s.hostsByTaskWithSources(taskID, " WHERE "+agentVisibleAssets("a.id"))
}

func (s *AssetStore) hostsByTaskWithSources(taskID int64, filter string) ([]string, error) {
	rows, err := s.db.Query(`WITH `+directTaskContextCTE+`,
context_assets AS (
  SELECT DISTINCT a.id
  FROM assets a
  WHERE EXISTS (SELECT 1 FROM context_tasks ctx WHERE ctx.task_id=ANY(a.task_ids))
  UNION
  SELECT ea.asset_id
  FROM exploration_anchors ea
  JOIN exploration_nodes en ON en.id=ea.node_id
  JOIN context_tasks ctx ON ctx.exploration_id=en.exploration_id
  JOIN assets anchored ON anchored.id=ea.asset_id
  WHERE EXISTS (SELECT 1 FROM context_tasks owner WHERE owner.task_id = ANY(anchored.task_ids))
  UNION
  SELECT DISTINCT a.id
  FROM assets a
  JOIN task_scope ts ON (
       (ts.kind='company'     AND a.company_id=ts.company_id)
    OR (ts.kind='root_domain' AND a.root_domain=ts.domain)
    OR (ts.kind='subdomain'   AND a.domain=ts.domain)
    OR (ts.kind IN ('ip','cidr') AND ts.net >>= try_inet(a.ip))
    OR (ts.kind='icp' AND (
         lower(regexp_replace(COALESCE(a.icp,''), '[[:space:]]+', '', 'g')) = ts.value
         OR lower(regexp_replace(COALESCE(a.app_icp,''), '[[:space:]]+', '', 'g')) = ts.value
       ))
  )
  JOIN context_tasks ctx ON ctx.task_id=ts.task_id
)
SELECT COALESCE(a.domain,''), COALESCE(a.ip,''), COALESCE(a.url,'')
FROM assets a JOIN context_assets ctx ON ctx.id=a.id`+filter, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	hosts := map[string]struct{}{}
	add := func(host string) {
		host = strings.TrimSpace(strings.ToLower(host))
		if host != "" {
			hosts[host] = struct{}{}
		}
	}
	for rows.Next() {
		var domain, ip, rawURL string
		if err := rows.Scan(&domain, &ip, &rawURL); err != nil {
			return nil, err
		}
		add(domain)
		add(ip)
		if parsed, err := url.Parse(rawURL); err == nil {
			add(parsed.Hostname())
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(hosts))
	for host := range hosts {
		out = append(out, host)
	}
	sort.Strings(out)
	return out, nil
}
