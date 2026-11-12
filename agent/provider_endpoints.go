package agent

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Hosted multi-model gateways reachable through the existing provider formats.
// Neither needs a dedicated wire format: Vercel AI Gateway and Amazon Bedrock both
// expose Anthropic Messages and OpenAI Chat Completions/Responses surfaces, so a
// profile only needs the right base URL, model ID style and API key. This file adds
// detection, pre-flight validation and actionable error hints around them.
//
// Verified against official documentation on 2026-10-21:
//
//	https://vercel.com/docs/ai-gateway/sdks-and-apis/openai-chat-completions
//	https://vercel.com/docs/ai-gateway/sdks-and-apis/anthropic-messages-api
//	https://vercel.com/docs/ai-gateway/authentication-and-byok
//	https://docs.aws.amazon.com/bedrock/latest/userguide/api-keys.html
//	https://docs.aws.amazon.com/bedrock/latest/userguide/endpoints.html
//	https://docs.aws.amazon.com/bedrock/latest/userguide/inference-messages-api.html
//	https://docs.aws.amazon.com/bedrock/latest/userguide/inference-chat-completions.html

// Endpoint kinds returned by EndpointKind.
const (
	EndpointVercelGateway  = "vercel-ai-gateway"
	EndpointBedrockRuntime = "bedrock-runtime"
	EndpointBedrockMantle  = "bedrock-mantle"
)

const vercelGatewayHost = "ai-gateway.vercel.sh"

var (
	bedrockRuntimeHost = regexp.MustCompile(`^bedrock-runtime\.[a-z0-9-]+\.amazonaws\.com(\.cn)?$`)
	bedrockMantleHost  = regexp.MustCompile(`^bedrock-mantle\.[a-z0-9-]+\.api\.aws$`)
	bedrockRegionToken = regexp.MustCompile(`\{region\}|<region>|<your-region>|\$\{?region\}?|\{\{region\}\}`)
)

// EndpointKind classifies a base URL as one of the supported hosted gateways.
// It returns "" for anything else (direct providers, self-hosted endpoints).
func EndpointKind(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	switch {
	case host == vercelGatewayHost:
		return EndpointVercelGateway
	case bedrockRuntimeHost.MatchString(host):
		return EndpointBedrockRuntime
	case bedrockMantleHost.MatchString(host):
		return EndpointBedrockMantle
	}
	return ""
}

// ValidateEndpoint checks a profile's format, base URL and model against the rules
// of the hosted gateways, before any request is sent. It returns nil for endpoints it
// does not recognise, so custom and self-hosted URLs are never blocked. The message
// names the exact fix because these URLs are easy to get subtly wrong (the provider
// appends "/v1/messages", "/chat/completions" or "/responses" itself).
func ValidateEndpoint(format, baseURL, model string) error {
	baseURL = strings.TrimSpace(baseURL)
	if bedrockRegionToken.MatchString(baseURL) {
		return errors.New("Base URL still contains a region placeholder. Replace it with your AWS Region, for example bedrock-runtime.us-east-1.amazonaws.com. The Region must match the one the Bedrock API key was created in")
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "" && u.Scheme != "https" && u.Scheme != "http") {
		return nil
	}
	path := strings.TrimRight(u.Path, "/")
	model = strings.TrimSpace(model)
	switch EndpointKind(baseURL) {
	case EndpointVercelGateway:
		return validateVercel(format, path, model)
	case EndpointBedrockRuntime:
		return validateBedrockRuntime(format, path, model)
	case EndpointBedrockMantle:
		return validateBedrockMantle(format, path, model)
	}
	return nil
}

func validateVercel(format, path, model string) error {
	switch format {
	case "anthropic":
		// The Anthropic provider appends /v1/messages, so a trailing /v1 doubles it.
		if path != "" {
			return errors.New("For Vercel AI Gateway with the Anthropic format, use the Base URL https://ai-gateway.vercel.sh with no path (a trailing /v1 would produce /v1/v1/messages)")
		}
	default:
		if path != "/v1" {
			return errors.New("For Vercel AI Gateway with an OpenAI format, use the Base URL https://ai-gateway.vercel.sh/v1")
		}
	}
	if model != "" && !strings.Contains(model, "/") {
		return fmt.Errorf("Vercel AI Gateway model IDs use the provider/model form, for example anthropic/claude-sonnet-5. %q has no provider prefix. Use Load models to see the exact IDs available to your key", model)
	}
	return nil
}

func validateBedrockRuntime(format, path, model string) error {
	switch format {
	case "anthropic":
		if path != "/anthropic" {
			return errors.New("For Amazon Bedrock with the Anthropic format, use https://bedrock-runtime.<region>.amazonaws.com/anthropic (the path must be exactly /anthropic)")
		}
	default:
		if path != "/openai/v1" {
			return errors.New("For Amazon Bedrock with an OpenAI format, use https://bedrock-runtime.<region>.amazonaws.com/openai/v1 (the path must be exactly /openai/v1)")
		}
	}
	return validateBedrockModel(model)
}

func validateBedrockMantle(format, path, model string) error {
	switch format {
	case "anthropic":
		if path != "/anthropic" {
			return errors.New("For Amazon Bedrock Mantle with the Anthropic format, use https://bedrock-mantle.<region>.api.aws/anthropic (the path must be exactly /anthropic)")
		}
	default:
		// Most Mantle models are served under /v1, a few under /openai/v1; the
		// server rejects the wrong one per model, which ExplainProviderError covers.
		if path != "/v1" && path != "/openai/v1" {
			return errors.New("For Amazon Bedrock Mantle with an OpenAI format, use https://bedrock-mantle.<region>.api.aws/v1 (a few models use /openai/v1; the model card lists which)")
		}
	}
	return validateBedrockModel(model)
}

func validateBedrockModel(model string) error {
	if strings.Contains(model, "/") {
		return fmt.Errorf("%q looks like a Vercel AI Gateway model ID. Amazon Bedrock IDs have no slash, for example anthropic.claude-sonnet-5 or us.anthropic.claude-sonnet-5 (cross-Region inference profile)", model)
	}
	return nil
}

// ModelListUnsupported reports why a gateway cannot list models through the standard
// GET /models call, so the UI can say so instead of showing a raw 404. It returns ""
// when listing should be attempted.
func ModelListUnsupported(baseURL string) string {
	if EndpointKind(baseURL) == EndpointBedrockRuntime {
		return "Amazon Bedrock's bedrock-runtime endpoint does not implement GET /models. Type the model ID yourself (see the model's card in the Bedrock console for its ID or inference profile ID), or point this profile at the bedrock-mantle endpoint, which can list models."
	}
	return ""
}

// ExplainProviderError appends an actionable hint to an LLM error when the endpoint is
// a recognised hosted gateway and the failure matches a known cause. The original
// error stays in the chain (errors.Is/As keep working). Unrecognised endpoints and
// unrecognised failures are returned unchanged.
func ExplainProviderError(baseURL string, err error) error {
	if err == nil {
		return nil
	}
	kind := EndpointKind(baseURL)
	if kind == "" {
		return err
	}
	hint := providerErrorHint(kind, strings.ToLower(err.Error()))
	if hint == "" {
		return err
	}
	return &hintedError{err: err, hint: hint}
}

type hintedError struct {
	err  error
	hint string
}

func (e *hintedError) Error() string { return e.err.Error() + " | Hint: " + e.hint }
func (e *hintedError) Unwrap() error { return e.err }

func providerErrorHint(kind, msg string) string {
	has := func(subs ...string) bool {
		for _, s := range subs {
			if strings.Contains(msg, s) {
				return true
			}
		}
		return false
	}
	switch kind {
	case EndpointVercelGateway:
		switch {
		case has("status 401", "status: 401", "unauthorized", "invalid api key", "invalid_api_key"):
			return "Vercel AI Gateway rejected the key. Create an AI Gateway API key in the Vercel dashboard (AI Gateway, API keys); a Vercel account token or a provider key will not work."
		case has("status 402", "insufficient", "credit", "payment required"):
			return "Your Vercel AI Gateway balance or budget is exhausted. Add credits or raise the key's budget in the Vercel dashboard."
		case has("status 403", "forbidden"):
			return "Vercel AI Gateway denied access. Check the key's team and project scope and whether the model needs a provider key (BYOK) on your team."
		case has("status 404", "model not found", "not_found", "no such model"):
			return "Vercel AI Gateway does not know this model ID. Use the provider/model form (for example anthropic/claude-sonnet-5) and pick one from Load models."
		case has("status 429", "rate limit", "too many requests"):
			return "Vercel AI Gateway rate limit reached. Lower the profile's requests per second/minute or retry shortly."
		}
	case EndpointBedrockRuntime, EndpointBedrockMantle:
		switch {
		case has("credential should be scoped to a valid region"):
			return "The Bedrock API key belongs to a different AWS Region than the Base URL. Long-term keys work only in the Region they were generated in: use that Region in the URL or generate a key in this Region."
		case has("expired", "security token", "token has expired"):
			return "The Bedrock API key has expired. Short-term keys last at most 12 hours; generate a new one or switch to a long-term key (exploration only)."
		case has("status 401", "status: 401", "unauthorized", "invalid api key", "invalid bearer", "authentication"):
			return "Amazon Bedrock rejected the API key. Paste the full Bedrock API key (not an AWS access key ID or secret) and check that it has not been deactivated."
		case has("isn't supported on this route", "is not supported on this route"):
			return "This model is served under the other path prefix on Mantle. Switch the Base URL between https://bedrock-mantle.<region>.api.aws/v1 and https://bedrock-mantle.<region>.api.aws/openai/v1 (the model card lists which)."
		case has("on-demand throughput", "inference profile"):
			return "This model needs a cross-Region inference profile ID on bedrock-runtime, for example us.anthropic.claude-sonnet-5 or global.anthropic.claude-sonnet-5, not the bare model ID."
		case has("status 403", "accessdenied", "not authorized", "access denied", "callwithbearertoken", "forbidden"):
			return "The IAM principal behind the key lacks permission. It needs bedrock:CallWithBearerToken and bedrock:InvokeModel (or bedrock-mantle:CallWithBearerToken and bedrock-mantle:CreateInference on Mantle), and access to the model must be enabled in the Bedrock console for this Region."
		case has("validationexception", "model identifier is invalid", "invalid model", "status 400") && has("model"):
			return "Amazon Bedrock does not recognise the model ID in this Region. Copy the ID or inference profile ID from the model's card, and confirm the model is offered in the URL's Region."
		case has("status 404", "unknownoperation", "model not found", "not found"):
			return "Amazon Bedrock could not find the model or path. Check the Base URL path (/anthropic or /openai/v1 on bedrock-runtime), the Region, and the model ID."
		case has("status 429", "throttl", "too many requests", "rate limit"):
			return "Amazon Bedrock throttled the request (per-model quota). Lower the profile's request rate or ask AWS for a quota increase."
		}
	}
	return ""
}
