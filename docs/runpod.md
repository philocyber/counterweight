# RunPod setup for Counterweight

This file is the source of truth for the in-app **Documentation → RunPod setup** guide. After editing it, run `node web/scripts/sync-runpod-docs.mjs`.


<!-- runpod-guide:start -->
One Secure Cloud GPU, one network volume, one vLLM server exposing an OpenAI-compatible Chat Completions API. Counterweight talks to it like any other OpenAI-compatible endpoint.

Throughout this guide, `<model-id>` is the Hugging Face id of the open-weight model you choose to serve. Pick an instruction-tuned model that supports tool calling in vLLM, and use the matching `--tool-call-parser` from the [vLLM tool-calling documentation](https://docs.vllm.ai/en/latest/features/tool_calling.html).

The local default Counterweight UI is [http://127.0.0.1:8787](http://127.0.0.1:8787). Docker installations use [https://localhost:8787](https://localhost:8787); use your configured management URL.

> **Before you provision:** GPU types, regions, availability and prices change. Check RunPod for current values and size the GPU for your model. Replace every secret and pod-id placeholder with your own value.

[Jump to the Counterweight profile](#runpod-profile) · [Troubleshooting](#runpod-troubleshooting)

### 1. Volume, once

Storage, then Network Volumes. Size it for your model weights plus headroom (100 GB covers most mid-size models). Choose the same region you will deploy the pod in. It mounts at `/workspace`. Do not create a second volume. Do not delete it when you stop the GPU.

### 2. One Secure Cloud pod

Pods, then Deploy, then Secure Cloud. Attach the volume from step 1. One GPU with enough memory for the model (FP8 quantization reduces the requirement). Do not start a second GPU because the first one looks idle or because a host was taken: migrate that pod, or stop it, then deploy the replacement.

### 3. Container, before you start it

- Image: a pinned `vllm/vllm-openai:<version>` tag. Do not switch to `latest` in the middle of a test.
- Container disk: 20 GB.
- Expose HTTP port: `8000`.
- Environment:

```dotenv
HF_HOME=/workspace/hf_cache
HF_HUB_ENABLE_HF_TRANSFER=1
VLLM_API_KEY=REPLACE_WITH_YOUR_OWN_SECRET
```

`VLLM_API_KEY` is the only key. Counterweight sends it as `Authorization: Bearer`.

- Container start command. Arguments only:

```text
--model <model-id> --host 0.0.0.0 --port 8000 --max-model-len 131072 --gpu-memory-utilization 0.90 --enable-prefix-caching --enable-auto-tool-choice --tool-call-parser <parser>
```

Add `--quantization fp8` if your GPU and model support it and you need the memory.

### 4. Start, then wait

The first boot downloads the weights onto the volume. Later boots read that cache and still compile CUDA graphs, which is the slow part. Port 8000 stays Initializing and the proxy returns 502 until vLLM is listening. Do not open a Counterweight task yet.

### 5. Prove the API

Run these commands in your local terminal. Set the key to the same secret configured on the pod; setting it in RunPod does not export it into your local shell. Replace the pod id after each migration.

```sh
export VLLM_API_KEY='REPLACE_WITH_YOUR_OWN_SECRET'
export RUNPOD_BASE_URL='https://<pod-id>-8000.proxy.runpod.net/v1'
```

```sh
curl --fail-with-body -sS -H "Authorization: Bearer $VLLM_API_KEY" \
  "$RUNPOD_BASE_URL/models"
```

Expect HTTP 200 listing `<model-id>` with `max_model_len` 131072. That response is what Counterweight needs. If you migrated, the pod id in that host changed: update the profile. Leave the old pod Stopped. Do not Terminate it.

<a id="runpod-profile"></a>

### 6. Counterweight profile, then make it the active one

[http://127.0.0.1:8787](http://127.0.0.1:8787), then LLM, then New, then Set active. Saving the row does not reload a process that is already running.

| Field | Value |
| :---- | :---- |
| Format | OpenAI (Chat Completions). Not Responses API. |
| Model | `<model-id>` |
| Base URL | `https://<pod-id>-8000.proxy.runpod.net/v1` |
| API key | the same value as `VLLM_API_KEY` |
| Context window (K) | `96` |
| Output limit | `8192` |
| Proxy, thinking, reasoning effort | leave unset |

### 7. Approvals, per task

A prompt offers four allows: this call only, this exact command for this task, this tool for this task, or every tool for this task. Standing choices are listed on that task's interception tab and can be revoked. Built-in deny rules still win. Do not insert global allow rules for Bash, shell, WebFetch, or web search. Those would apply to every task.

### 8. When you stop

Stop the running pod. Leave the volume. Leave stopped pods stopped. Terminate removes the container. A running GPU bills until you stop it.

<a id="runpod-troubleshooting"></a>

### Troubleshooting

1. `--max-model-len` is the whole window: prompt plus reply. It is not an output cap, and it is not the Context window (K) field in Counterweight. Once the tool schemas are attached, the worker prompt is already about 29,000 tokens. With a 32,768 window, a 29,000-token prompt plus an 8,192-token reply exceeds the window and vLLM returns HTTP 400. Compaction cannot delete the tool definitions, so the worker ends in `prompt_too_long`. Use 131072 if your model and GPU allow it.
2. Output limit `0` in Counterweight omits `max_tokens`. vLLM then asks for the entire `--max-model-len` as the reply and returns 400 even for a short chat.
3. Context window (K) is only Counterweight's local compaction budget. Anything under 32 is raised to 32. It does not subtract the reply. With `--max-model-len 131072` and an output limit of 8192, set Context window (K) to 96 so compaction stays under `131072 - 8192`.
4. The Responses API template in the new-profile form does not match this server. The format is OpenAI Chat Completions.
5. The image entrypoint is already `vllm serve`. The RunPod field is arguments only. Do not prefix `vllm serve` or a shell.
6. Do not pass `repo@revision` values (for example in `--lora-modules`) in the start command. vLLM passes that string to Hugging Face as a repo id, `@` is rejected, and the container exits after the base model has loaded.
7. Migrating a pod creates a new pod id. The old proxy URL returns an empty HTTP 404. 502 means the route exists and vLLM is not listening yet, which is normal while it boots. 401 means `VLLM_API_KEY` did not stick.
8. Saving an edit on a running pod resets the container. The network volume keeps the weights. The container disk does not, so CUDA graphs compile again. That is several minutes of GPU time with no API. Wait until port 8000 says Ready and `GET /v1/models` shows the expected `max_model_len` before pointing Counterweight at it.

### Do not

- Do not prefix the start command with `vllm serve`.
- Do not put `repo@revision` in the start command.
- Do not set `--max-model-len 32768` for Counterweight.
- Do not leave Output limit at 0.
- Do not set Context window (K) to 32 and expect that to be the API window.
- Do not use the Responses API profile template.
- Do not leave Counterweight pointed at a stopped or migrated pod id.
- Do not deploy a second pod or a second volume while one GPU is billing.
- Do not Terminate a stopped pod to clean up during a test.
<!-- runpod-guide:end -->
