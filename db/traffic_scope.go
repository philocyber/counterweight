package db

import (
	"encoding/json"
	"net"
	"strings"
)

// TrafficHostScope is the set of hosts a task may read captured traffic for.
// It is built only from operator-authorized sources: hosts of the assets in the
// operator read grants, operator-written scope rows and the read grants captured
// when a company or ICP scope was authorized. Assets an agent recorded for the
// task, and coverage proposals written by agents or the automatic scoper, add
// nothing: an agent must not be able to widen what it reads.
type TrafficHostScope struct {
	exact   map[string]struct{}
	domains []string
	nets    []*net.IPNet
}

// Allows reports whether traffic recorded for host (a bare host name or IP, as
// stored by the capture store) is readable under the scope.
func (s *TrafficHostScope) Allows(host string) bool {
	if s == nil {
		return false
	}
	host = strings.Trim(scopeHostKey(host), "[]")
	if host == "" {
		return false
	}
	if _, ok := s.exact[host]; ok {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		for _, n := range s.nets {
			if n.Contains(ip) {
				return true
			}
		}
		return false
	}
	for _, d := range s.domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// scopeHostKey is the form hosts are compared in: the canonical ASCII name (see
// CanonicalHost), so a U-label and its A-label are one host. A name that cannot
// be converted keeps its lowercase text.
func scopeHostKey(host string) string {
	if canonical, err := CanonicalHost(host); err == nil {
		return canonical
	}
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func (s *TrafficHostScope) addDomain(d string) {
	d = scopeHostKey(d)
	if d != "" {
		s.domains = append(s.domains, d)
	}
}

func (s *TrafficHostScope) addNet(cidr string) {
	cidr = strings.TrimSpace(cidr)
	if _, n, err := net.ParseCIDR(cidr); err == nil {
		s.nets = append(s.nets, n)
	} else if ip := net.ParseIP(cidr); ip != nil {
		s.exact[ip.String()] = struct{}{}
	}
}

// TrafficHostScopeForTask resolves which hosts the task's agents may read
// captured traffic for: the task and its direct source tasks, operator grants
// only. An error means the scope could not be established; callers must deny.
func (s *AssetStore) TrafficHostScopeForTask(taskID int64) (*TrafficHostScope, error) {
	out := &TrafficHostScope{exact: map[string]struct{}{}}
	hosts, err := s.hostsByTaskWithSources(taskID, " WHERE a.id IN ("+scopeGrantIDsSQL+")")
	if err != nil {
		return nil, err
	}
	for _, h := range hosts {
		out.exact[scopeHostKey(h)] = struct{}{}
	}
	rows, err := s.db.Query(`WITH `+directTaskContextCTE+`
SELECT ts.kind, COALESCE(ts.domain,''), COALESCE(ts.net::text,''), COALESCE(ts.read_grant::text,'')
FROM task_scope ts JOIN context_tasks ctx ON ctx.task_id=ts.task_id
WHERE ts.source='manual' AND ts.kind IN ('company','icp','root_domain','subdomain','ip','cidr')`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var kind, domain, network, grant string
		if err := rows.Scan(&kind, &domain, &network, &grant); err != nil {
			return nil, err
		}
		switch kind {
		case "root_domain":
			out.addDomain(domain)
		case "subdomain":
			out.exact[scopeHostKey(domain)] = struct{}{}
		case "ip", "cidr":
			out.addNet(network)
		case "company", "icp":
			var g struct {
				Scope []struct {
					Kind   string `json:"kind"`
					Domain string `json:"domain"`
					Net    string `json:"net"`
				} `json:"scope"`
			}
			if grant == "" || json.Unmarshal([]byte(grant), &g) != nil {
				continue
			}
			for _, entry := range g.Scope {
				switch entry.Kind {
				case "domain":
					out.addDomain(entry.Domain)
				case "ip", "cidr":
					out.addNet(entry.Net)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
