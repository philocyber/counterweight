package agent

import (
	"errors"
	"strings"
	"testing"
)

func TestEndpointKind(t *testing.T) {
	cases := map[string]string{
		"https://ai-gateway.vercel.sh/v1":                                 EndpointVercelGateway,
		"https://ai-gateway.vercel.sh":                                    EndpointVercelGateway,
		"https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1":       EndpointBedrockRuntime,
		"https://bedrock-runtime.eu-west-2.amazonaws.com/anthropic":       EndpointBedrockRuntime,
		"https://bedrock-mantle.us-west-2.api.aws/v1":                     EndpointBedrockMantle,
		"https://bedrock-mantle.us-west-2.api.aws/anthropic":              EndpointBedrockMantle,
		"https://api.openai.com/v1":                                       "",
		"https://api.z.ai/api/paas/v4":                                    "",
		"":                                                                "",
		"https://evil.example/bedrock-runtime.us-east-1.amazonaws.com":    "",
		"https://bedrock-runtime.us-east-1.amazonaws.com.evil.example/v1": "",
		"not a url": "",
	}
	for in, want := range cases {
		if got := EndpointKind(in); got != want {
			t.Errorf("EndpointKind(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidateEndpoint(t *testing.T) {
	ok := []struct{ format, url, model string }{
		{"openai", "https://ai-gateway.vercel.sh/v1", "anthropic/claude-sonnet-5"},
		{"anthropic", "https://ai-gateway.vercel.sh", "openai/gpt-6-astra"},
		{"openai-responses", "https://ai-gateway.vercel.sh/v1", ""},
		{"anthropic", "https://bedrock-runtime.us-east-1.amazonaws.com/anthropic", "us.anthropic.claude-sonnet-5"},
		{"openai", "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1", "openai.gpt-oss-120b-1:0"},
		{"openai", "https://bedrock-mantle.us-east-1.api.aws/v1", "deepseek.v3.2"},
		{"openai", "https://bedrock-mantle.us-east-1.api.aws/openai/v1", ""},
		{"anthropic", "https://bedrock-mantle.us-east-1.api.aws/anthropic", "anthropic.claude-sonnet-5"},
		// Unrecognised endpoints are never blocked.
		{"openai", "https://api.z.ai/api/paas/v4", "glm-5.3"},
		{"anthropic", "", "claude-opus-5-5"},
		{"openai", "http://localhost:11434/v1", "llama/3"},
	}
	for _, c := range ok {
		if err := ValidateEndpoint(c.format, c.url, c.model); err != nil {
			t.Errorf("ValidateEndpoint(%q, %q, %q) = %v, want nil", c.format, c.url, c.model, err)
		}
	}

	bad := []struct{ format, url, model, want string }{
		{"anthropic", "https://ai-gateway.vercel.sh/v1", "a/b", "no path"},
		{"openai", "https://ai-gateway.vercel.sh", "a/b", "/v1"},
		{"openai", "https://ai-gateway.vercel.sh/v1", "claude-sonnet-5", "provider/model"},
		{"anthropic", "https://bedrock-runtime.us-east-1.amazonaws.com", "x", "/anthropic"},
		{"openai", "https://bedrock-runtime.us-east-1.amazonaws.com/v1", "x", "/openai/v1"},
		{"openai", "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1", "anthropic/claude", "no slash"},
		{"openai", "https://bedrock-mantle.us-east-1.api.aws/anthropic", "x", "/v1"},
		{"anthropic", "https://bedrock-mantle.us-east-1.api.aws/v1", "x", "/anthropic"},
		{"openai", "https://bedrock-runtime.{region}.amazonaws.com/openai/v1", "x", "region placeholder"},
		{"openai", "https://bedrock-mantle.<your-region>.api.aws/v1", "x", "region placeholder"},
	}
	for _, c := range bad {
		err := ValidateEndpoint(c.format, c.url, c.model)
		if err == nil {
			t.Errorf("ValidateEndpoint(%q, %q, %q) = nil, want error containing %q", c.format, c.url, c.model, c.want)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("ValidateEndpoint(%q, %q, %q) = %q, want it to contain %q", c.format, c.url, c.model, err, c.want)
		}
	}
}

func TestModelListUnsupported(t *testing.T) {
	if ModelListUnsupported("https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1") == "" {
		t.Error("bedrock-runtime has no GET /models and must say so")
	}
	for _, u := range []string{
		"https://bedrock-mantle.us-east-1.api.aws/v1",
		"https://ai-gateway.vercel.sh/v1",
		"https://api.openai.com/v1",
		"",
	} {
		if got := ModelListUnsupported(u); got != "" {
			t.Errorf("ModelListUnsupported(%q) = %q, want empty", u, got)
		}
	}
}

func TestExplainProviderError(t *testing.T) {
	vercel := "https://ai-gateway.vercel.sh/v1"
	runtime := "https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1"
	mantle := "https://bedrock-mantle.us-east-1.api.aws/v1"
	cases := []struct{ base, msg, want string }{
		{vercel, "openai: status 401: Invalid API key", "AI Gateway API key"},
		{vercel, "openai: status 402: insufficient credits", "balance"},
		{vercel, "openai: status 404: model not found", "provider/model"},
		{vercel, "openai: status 429: rate limit exceeded", "rate limit"},
		{runtime, "status 403: Credential should be scoped to a valid region", "different AWS Region"},
		{runtime, "status 403: The security token included in the request is expired", "expired"},
		{runtime, "status 403: AccessDeniedException: not authorized to perform bedrock:InvokeModel", "bedrock:CallWithBearerToken"},
		{runtime, "status 400: Invocation of model ID with on-demand throughput isn't supported", "inference profile"},
		{mantle, "status 400: model `x` isn't supported on this route", "/openai/v1"},
		{mantle, "status 429: ThrottlingException", "throttled"},
		{mantle, "status 401: invalid api key", "API key"},
	}
	for _, c := range cases {
		got := ExplainProviderError(c.base, errors.New(c.msg))
		if !strings.Contains(got.Error(), c.msg) {
			t.Errorf("%q: original message lost: %q", c.msg, got)
		}
		if !strings.Contains(got.Error(), "Hint: ") || !strings.Contains(got.Error(), c.want) {
			t.Errorf("%q: hint %q missing from %q", c.msg, c.want, got)
		}
	}
}

func TestExplainProviderErrorLeavesOthersAlone(t *testing.T) {
	orig := errors.New("openai: status 401: Invalid API key")
	if got := ExplainProviderError("https://api.openai.com/v1", orig); got != orig {
		t.Errorf("direct provider error must be returned unchanged, got %v", got)
	}
	unknown := errors.New("connection reset by peer")
	if got := ExplainProviderError("https://ai-gateway.vercel.sh/v1", unknown); got != unknown {
		t.Errorf("unrecognised failure must be returned unchanged, got %v", got)
	}
	if ExplainProviderError("https://ai-gateway.vercel.sh/v1", nil) != nil {
		t.Error("nil error must stay nil")
	}
	wrapped := ExplainProviderError("https://ai-gateway.vercel.sh/v1", orig)
	if !errors.Is(wrapped, orig) {
		t.Error("original error must stay in the chain")
	}
}
