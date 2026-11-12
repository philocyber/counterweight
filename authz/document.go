package authz

import (
	"encoding/json"
	"math"
)

// Document is a policy as plain data, in file order, for an editor that shows
// it as a form instead of text. It says what the file says: a reference stays
// a reference, so a Document holds no credential value.
type Document struct {
	Version  int               `json:"version"`
	Target   Target            `json:"target"`
	Personas []DocumentPersona `json:"personas"`
	Cases    []*Case           `json:"cases"`
}

// DocumentPersona is a persona with its name.
type DocumentPersona struct {
	Key string `json:"key"`
	*Persona
}

// Outline reads policy text as a Document. It does not validate the policy:
// an editor must be able to show one that still has problems. ok is false
// when the text is not a policy at all, or holds a value an editor in a
// browser would not hand back unchanged (a number beyond its exact range, a
// date, a key that is not text); such a policy is edited as text.
func Outline(data []byte) (doc *Document, ok bool) {
	p, err := decode(data)
	if err != nil {
		return nil, false
	}
	doc = &Document{Version: p.Version, Target: p.Target, Personas: []DocumentPersona{}, Cases: []*Case{}}
	for _, name := range p.order {
		per := p.Personas[name]
		if per.Login != nil && !exactInBrowser(per.Login.Request.JSON) || per.Identity != nil && !exactInBrowser(per.Identity.Request.JSON) {
			return nil, false
		}
		doc.Personas = append(doc.Personas, DocumentPersona{Key: name, Persona: per})
	}
	for _, c := range p.Cases {
		if c == nil || !exactInBrowser(c.Request.JSON) {
			return nil, false
		}
		doc.Cases = append(doc.Cases, c)
	}
	if _, err := json.Marshal(doc); err != nil {
		return nil, false
	}
	return doc, true
}

// maxExactInt is the largest integer a browser number holds exactly (2^53-1).
const maxExactInt = 1<<53 - 1

// exactInBrowser reports whether a JSON body of a policy survives a trip
// through a browser: text keys, and no number it would round.
func exactInBrowser(v any) bool {
	switch t := v.(type) {
	case nil, string, bool:
		return true
	case int:
		return t >= -maxExactInt && t <= maxExactInt
	case int64:
		return t >= -maxExactInt && t <= maxExactInt
	case uint64:
		return t <= maxExactInt
	case float64:
		return !math.IsNaN(t) && !math.IsInf(t, 0)
	case map[string]any:
		for _, x := range t {
			if !exactInBrowser(x) {
				return false
			}
		}
		return true
	case []any:
		for _, x := range t {
			if !exactInBrowser(x) {
				return false
			}
		}
		return true
	}
	return false
}
