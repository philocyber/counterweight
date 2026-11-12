# Choosing and connecting a model

Counterweight does not ship a model. Every agent (planner, workers, code reviewer, verifier,
reporter) calls the model profile you connect under **LLM**. This guide helps you decide where that
model runs, then gives the shortest working setup for each option.

Templates and gateway details are in [llm-providers.md](llm-providers.md); the complete
rented-GPU walkthrough is in [runpod.md](runpod.md).

> [!NOTE]
> The project has not benchmarked models against each other. This guide states what the agents
> require and what each option involves. It does not rank models: confirm the one you choose with
> the [acceptance check](#acceptance-check) at the end before you rely on it.

## What the agents need from a model

| Requirement | Why |
| --- | --- |
| **Tool (function) calling**, reliable over many turns | Agents act only through tools: reading files, running commands, recording findings. A model that answers in prose instead of calling a tool makes no progress. |
| **A large context window: 128K tokens or more is the practical starting point** | The instructions plus tool definitions already take roughly 29,000 tokens before any history or evidence (measured on the self-hosted setup in [runpod.md](runpod.md)). Counterweight accepts a declared window as low as 32K, but that leaves almost nothing for the work itself. |
| **An explicit output limit on self-hosted servers** | Some servers reserve the whole window for the reply when no limit is sent, and then reject the request. Set **Output limit** (for example `8192`) on self-hosted profiles. |
| **Streaming or non-streaming chat over one of three formats** | Anthropic Messages, OpenAI Chat Completions, or OpenAI Responses. Anything that speaks one of them can be connected with a base URL. |

Vision is not required.

## Decide where inference runs

| Option | What leaves your network | Setup effort | Choose it when |
| --- | --- | --- | --- |
| [Hosted API](#hosted-api) (Anthropic, OpenAI, Z.ai) | Prompts, tool output and target data go to that provider | Lowest: paste a key | You want the quickest start and your data policy allows the provider |
| [Gateway](#gateway) (Vercel AI Gateway, Amazon Bedrock) | Same, through the gateway to the model provider | Low | Billing or access already runs through a gateway, or you want one key for several models |
| [ChatGPT / Codex sign-in](#chatgpt--codex-sign-in) | Same, to OpenAI under your workspace's terms | Low, but needs the Codex CLI on the backend host | You already have a subscription and no API key |
| [Local model on your workstation](#local-model-on-your-workstation) (Ollama, LM Studio, llama.cpp) | Nothing | Medium, and depends on your hardware | Data must not leave the machine and you accept slower, less capable runs |
| [Self-hosted GPU server](#self-hosted-gpu-server) (vLLM) | Nothing outside your infrastructure | High | A team shares one in-house inference server |
| [Rented GPU](#rented-gpu) (vLLM on RunPod) | Data goes to the GPU host you rent | High | You want open-weight models without buying hardware |

Whatever you choose, the data boundary is the same sentence: **everything an agent reads is sent
to the model you configure**, including source code, HTTP responses from your targets and command
output. Pick an option your organization is allowed to send that material to.

## Hosted API

1. **LLM → New**, pick the provider. The template fills format, base URL and model.
2. Paste the API key, click **Test connection**, save, then **Set active**.

To start with a key already in place, set `ANTHROPIC_API_KEY` or `OPENAI_API_KEY` in `.env`
(Docker) or the environment (native) before the first start; a default profile is created from it.

## Gateway

Follow [Gateways: one key, many models](llm-providers.md#gateways-one-key-many-models). The two
things that go wrong most often are the base URL path for the chosen format and the model ID style;
the form warns about both before you save.

## ChatGPT / Codex sign-in

Follow [Codex / ChatGPT subscription](llm-providers.md#codex--chatgpt-subscription). The Codex CLI
must be installed on the machine that runs the Counterweight backend. It is not part of the Docker
image, so this option suits native installs.

## Local model on your workstation

Any server that exposes the OpenAI Chat Completions API works. Create the profile with **LLM → New**
and these values:

| Field | Value |
| --- | --- |
| Format | OpenAI (Chat Completions) |
| Base URL | the server's `/v1` address (table below) |
| Model | the model name exactly as the server lists it (**Load models** fetches the list) |
| API key | any non-empty text, for example `local`. The field is required even when the server ignores it |
| Context window (K) | the context size you configured on the server, minus the output limit |
| Output limit | `8192` |

| Server | Default base URL | Before you connect |
| --- | --- | --- |
| [Ollama](https://docs.ollama.com/capabilities/tool-calling) | `http://localhost:11434/v1` | Use a model whose page lists tool support, and raise the context length: Ollama's default is far below what the agents need. Set it for the server or in the model's parameters as described in Ollama's documentation. |
| [LM Studio](https://lmstudio.ai/docs/developer/openai-compat/tools) | `http://localhost:1234/v1` | Start the local server, load a tool-capable model and set its context length in the load settings. |
| [llama.cpp](https://github.com/ggml-org/llama.cpp/blob/master/docs/function-calling.md) (`llama-server`) | `http://localhost:8080/v1` | Start it with the chat-template and function-calling options its documentation gives for your model, and an explicit context size. |

**Counterweight in Docker, model server on the host.** Inside the container `localhost` is the
container itself. Use `http://host.docker.internal:<port>/v1` as the base URL; the Compose file maps
that name to the host on Linux as well as on Docker Desktop. On Linux the model server must also
listen on an address the container can reach (not only `127.0.0.1`); keep it firewalled from other
machines.

**Expect to tune.** Start with one work agent (**System settings → Work agents per task → 1**), a
small target and the `light` review depth. Local models differ widely in how reliably they call
tools; a model that passes **Test connection** can still fail the acceptance check below. These
servers have not been verified end to end by the project (see [VERIFICATION.md](VERIFICATION.md)).

## Self-hosted GPU server

vLLM exposes the same Chat Completions API. The sizing rules, the start command, the tool-call
parser requirement and the profile values are in
[Self-hosted vLLM on RunPod](llm-providers.md#self-hosted-vllm-on-runpod); they apply unchanged to a
server you own. Put the server behind TLS and an API key if anything other than the Counterweight
host can reach it.

## Rented GPU

Follow [RunPod setup for Counterweight](runpod.md) from start to finish. Remember that a stopped
pod still bills for storage, and that a migrated pod gets a new address you must update in the
profile.

## Keeping cost and load under control

Give each task a budget and, as a second line of defence, set a limit on the provider side:

| Control | Where | Effect |
| --- | --- | --- |
| Task budget | **New task → advanced options**, the task header, and a default under **System settings → General** | The task pauses when it has used its token budget (or its cost budget, once you enter prices on the model profile) and continues when you raise it. |
| Work agents per task | **System settings** | Fewer parallel workers means fewer simultaneous requests. Use 1 while evaluating a model. |
| Review depth | `/code-review owner/repo light` | `light` reviews only security-critical files; `comprehensive` reads far more. |
| Request rate | The profile's rate fields | Keeps you under provider rate limits instead of failing on 429 responses. |
| A different model per agent | **Agents → model binding** | Bind a cheaper profile to routine roles and keep the strongest for review and verification. |
| Run budget | **Agents → run budget** | Caps how long a single agent keeps going. |
| Usage | **Dashboard** token panels, **LLM recordings** | Shows what each profile actually consumed, including cached tokens. |

Cost per assessment depends on the size of the target, the depth and the model's pricing. Measure it
on a small run before you start a large one.

## Acceptance check

A passing **Test connection** only proves the endpoint answers. Before trusting a model with a real
assessment, run this once:

1. Activate the profile (**Set active**).
2. In **Chat**, send `/code-review philocyber/Vulnerable-Flask-App light`. It is a small, intentionally
   vulnerable public training app.
3. Open the task. Within a few minutes the **Session** tab should show tool calls (file reads,
   searches) and the **Findings** tab should start to fill.

| What you see | Likely cause |
| --- | --- |
| The agent writes plans in prose and never calls a tool | The model or server does not support tool calling, or the server's tool-call parser does not match the model |
| HTTP 400 mentioning context length or `max_tokens` | Context window (K) plus Output limit exceed what the server was started with; lower one or raise the server's context |
| The task stalls after the first long step | The context window is too small for the tool definitions plus history; use a larger window |
| HTTP 401 or 403 | Wrong key, wrong key type for the gateway, or the model is not enabled for the account |
| HTTP 429 | Lower the profile's request rate or the number of work agents |

When the check produces findings with code evidence, the model is usable. Whether its findings are
good enough is something only a review of the results can tell you: verify before acting, with any
model.
