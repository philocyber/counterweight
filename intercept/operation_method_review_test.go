package intercept

import (
	"encoding/json"
	"testing"
)

func TestUnresolvedRequestMethodsRequireHumanApproval(t *testing.T) {
	for _, command := range []string{
		`curl https://fixture.invalid/resource`,
		`curl.exe https://fixture.invalid/resource`,
		`'C:\Windows\System32\CURL.EXE' https://fixture.invalid/resource`,
		`wget.exe https://fixture.invalid/resource`,
		`curl -s -q https://fixture.invalid/resource`,
		`curl -- https://fixture.invalid/resource/-q`,
		`curl -q -K settings.conf https://fixture.invalid/resource`,
		`curl -q --requ=DELETE https://fixture.invalid/resource`,
		`curl -q --conf=settings.conf https://fixture.invalid/resource`,
		`curl -q --variable method=DELETE --expand-request '{{method}}' https://fixture.invalid/resource`,
		`curl -q --expand-request=DELETE https://fixture.invalid/resource`,
		`curl -q --expand-config='{{config}}' https://fixture.invalid/resource`,
		`curl -q --expand-requ='{{method}}' https://fixture.invalid/resource`,
		`wget https://fixture.invalid/resource`,
		`wget --header '--no-config' https://fixture.invalid/resource`,
		`wget -- --no-config https://fixture.invalid/resource`,
		`wget --no-config --config=settings.conf https://fixture.invalid/resource`,
		`wget --no-config --meth=DELETE https://fixture.invalid/resource`,
		`wget --no-config --conf=settings.conf https://fixture.invalid/resource`,
		`wget --no-config --exec='method=DELETE' https://fixture.invalid/resource`,
		`curl -X "$METHOD" https://fixture.invalid/resource`,
		`curl --request="$METHOD" https://fixture.invalid/resource`,
		`curl -sX"$METHOD" https://fixture.invalid/resource`,
		`curl "$FLAGS" https://fixture.invalid/resource`,
		`curl -H $HEADERS https://fixture.invalid/resource`,
		`curl -K settings.conf https://fixture.invalid/resource`,
		`curl -sKsettings.conf https://fixture.invalid/resource`,
		`curl --config=settings.conf https://fixture.invalid/resource`,
		`env MODE=fixture wget --method "$METHOD" https://fixture.invalid/resource`,
		`wget --method="$METHOD" https://fixture.invalid/resource`,
		`wget --config=settings.conf https://fixture.invalid/resource`,
		`wget -e 'method=DELETE' https://fixture.invalid/resource`,
		`wget --execute='method=GET' https://fixture.invalid/resource`,
		`curl * https://fixture.invalid/resource`,
		`curl --header "$@" https://fixture.invalid/resource`,
		`curl --header "${HEADERS[@]}" https://fixture.invalid/resource`,
		`curl --data $(printf '%s' flags) https://fixture.invalid/resource`,
		`bash -c 'curl -X "$METHOD" https://fixture.invalid/resource'`,
		`printf '%s\n' 'curl -K settings.conf https://fixture.invalid/resource' | bash`,
	} {
		for _, tool := range []string{"Bash", "shell_send"} {
			input, _ := json.Marshal(map[string]string{"command": command, "text": command})
			i := &Interceptor{cached: []compiledRule{semanticFixture(`method: 'DELETE'`, "deny")}}
			if dec, hit := i.Match(tool, input); !hit || dec.Action != "ask" || dec.ModelFallback || dec.TimeoutAction != "deny" {
				t.Errorf("unresolved request silently passed: %q: %+v %v", command, dec, hit)
			}
			i.cached = []compiledRule{semanticFixture(`fixture.invalid`, "allow"), semanticFixture(`method: 'DELETE'`, "deny")}
			if dec, hit := i.Match(tool, input); !hit || dec.Action != "allow" {
				t.Error("explicit operator allow lost")
			}
		}
	}
}

func TestRequestMethodReviewPreservesOptionData(t *testing.T) {
	for _, command := range []string{
		`curl -q -H "$HEADER" --url "$URL"`,
		`curl.exe -q https://fixture.invalid/resource`,
		`curl -q --request=GET --request-target /resource https://fixture.invalid/resource`,
		`curl -q --header '--expand-request=DELETE' https://fixture.invalid/resource`,
		`wget --no-config --header '--meth=DELETE' https://fixture.invalid/resource`,
		`wget.exe --no-config https://fixture.invalid/resource`,
		`curl -q -H"Authorization: $TOKEN" https://fixture.invalid/resource`,
		`curl --disable "--header=Authorization: $TOKEN" https://fixture.invalid/resource`,
		`curl -q --data "$BODY" https://fixture.invalid/resource`,
		`curl -q -- "$URL"`,
		`wget --no-config --header "$HEADER" --method GET -- "$URL"`,
		`wget --no-config --body-data "$BODY" --method GET https://fixture.invalid/resource`,
		`wget --no-config --header '--method=DELETE' https://fixture.invalid/resource`,
		`echo 'curl --config=settings.conf'`,
		`curl -q --data '--config=settings.conf' https://fixture.invalid/resource`,
		`curl -q --header='literal *?[ header' https://fixture.invalid/resource`,
		`curl -q -H"literal *?[ $TOKEN" https://fixture.invalid/resource`,
		`curl -q --url "$(printf '%s' https://fixture.invalid/resource)"`,
	} {
		input, _ := json.Marshal(map[string]string{"command": command})
		i := &Interceptor{cached: []compiledRule{semanticFixture(`method: 'DELETE'`, "deny")}}
		assertUnconfinedShellReview(t, i, input)
	}
	input := []byte(`{"command":"wget -H --method=DE'LE'TE https://fixture.invalid/resource"}`)
	i := &Interceptor{cached: []compiledRule{semanticFixture(`method: 'DELETE'`, "deny")}}
	if dec, hit := i.Match("Bash", input); !hit || dec.Action != "deny" {
		t.Errorf("wget's flag swallowed the following method option: %+v %v", dec, hit)
	}
}

func TestRequestMethodReviewPolicyControls(t *testing.T) {
	input := []byte(`{"command":"curl -X \"$METHOD\" https://fixture.invalid/resource"}`)
	for _, rules := range [][]compiledRule{{}, {semanticFixture("unmatched", "allow")}} {
		i := &Interceptor{cached: rules}
		if _, hit := i.Match("Bash", input); hit {
			t.Fatal("inactive restrictive policy gained a decision")
		}
	}
	i := &Interceptor{cached: []compiledRule{semanticFixture(`method: 'DELETE'`, "deny")}}
	if _, hit := i.Match("Write", input); hit {
		t.Fatal("non-execution tool entered request-method approval")
	}
}

func TestNativeHTTPNamesRetainExplicitMethodDenial(t *testing.T) {
	for _, command := range []string{
		`curl.exe -q --request=DELETE https://fixture.invalid/resource`,
		`'C:\Windows\System32\CURL.EXE' -q -X 'DELETE' https://fixture.invalid/resource`,
		`wget.exe --no-config --method=DELETE https://fixture.invalid/resource`,
	} {
		input, _ := json.Marshal(map[string]string{"command": command})
		i := &Interceptor{cached: []compiledRule{semanticFixture(`method: 'DELETE'`, "deny")}}
		if decision, hit := i.Match("Bash", input); !hit || decision.Action != "deny" {
			t.Fatalf("native HTTP method denial lost: %q %+v %v", command, decision, hit)
		}
	}
}
