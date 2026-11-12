export const en = {
  subscriptionModelPlaceholder: "Choose a model after sign-in",
  subscriptionModelsLoading: "Loading models for this ChatGPT account…",
  subscriptionModelsHelp:
    "Models come from the native Codex catalog. A successful connection test confirms workspace access.",
  subscriptionDaybreakHelp:
    "Uses the selected model’s advertised Codex access program. Unavailable models are disabled while Daybreak is on. OpenAI verifies workspace approval on each request.",
  authMethod: "Connection type",
  apiKeyMethod: "API key",
  subscriptionMethod: "Codex / ChatGPT subscription",
  connectCodex: "Connect Codex",
  continueChatGPT: "Continue with ChatGPT",
  subscriptionAccount: "ChatGPT account",
  subscriptionNewAccount: "Add a ChatGPT account",
  subscriptionHelp:
    "Use your ChatGPT account through the local Codex runtime, including Enterprise workspaces with Codex access enabled. Complete browser sign-in, select a model, and save. The Codex CLI must be installed on the backend host. Open Counterweight on that same computer for browser sign-in.",
  subscriptionWaiting: "Waiting for browser sign-in…",
  subscriptionCancel: "Cancel",
  subscriptionOpenBrowser: "Reopen sign-in",
  subscriptionUsage: "Manage ChatGPT usage and app access",
  subscriptionConnected: "ChatGPT subscription connected. Choose a model and save the profile.",
  subscriptionDisconnected: "Disconnected",
  subscriptionSignInFirst: "Connect a ChatGPT account before saving this profile.",
  popupBlocked: "Your browser blocked the sign-in window. Allow popups for Counterweight and try again.",
  loginCancelled: "ChatGPT sign-in was cancelled or expired.",
  template: "Configuration template (new profile only)",
  chooseTemplate: "Choose a template…",
  general: "Z.ai GLM-5.3 · General API (recommended)",
  coding: "Z.ai GLM-5.3 · Coding Plan (reference)",
  introduction:
    "Optional: prefill connection fields, then enter a profile name and your API key. Your key, proxy, and name are kept. Selection does not save or activate a profile. Save when ready, then activate separately. The model remains editable.",
  generalHelp:
    "Uses the General API endpoint and API billing, separate from Coding Plan subscription quota. Confirm model access and billing for your Z.ai account.",
  codingHelp:
    "Reference only: Coding Plan uses a separate endpoint and subscription quota. Z.ai restricts it to officially supported tools; Counterweight is not listed. Obtain Z.ai authorization before using this endpoint with Counterweight.",
  modelHelp:
    "GLM-5.3 is text-only with a 1M-token context. Reasoning must stay enabled; supported effort levels are low, high, and max. This template selects max and leaves the output limit at 0 (provider default). Review these settings if you change the model.",
  officialModel: "GLM-5.3 model guide",
  officialPolicy: "Coding Plan usage policy",
  claudeGroup: "Anthropic Claude",
  zaiGroup: "Z.ai GLM",
  openaiGroup: "OpenAI",
  openaiSol: "GPT-6.1 Sol · planner or workers",
  openaiLuna: "GPT-6 Luna · workers, light agents and the interception judge",
  openaiHelp:
    "Uses the Responses API, which Counterweight needs here: GPT-6.1 Sol does not support function calling on Chat Completions. Base URL stays empty for the official endpoint. The window is 1.05M tokens with 128K output, so Context window is set to 922 (the input share). Effort is the documented default, medium. Add your OpenAI API key.",
  openaiOfficial: "Model page",
  claudeFable: "Claude Fable 5.1 · planner for the hardest tasks",
  claudeOpus: "Claude Opus 5.5 · planner and main agent (recommended)",
  claudeSonnet: "Claude Sonnet 5.5 · workers",
  claudeHaiku: "Claude Haiku 5.5 · interception judge and light agents",
  claudeHelp:
    "Uses the official Anthropic endpoint, so Base URL stays empty. The model has a 1M-token context and 128K output, so Context window is set to 1000. Thinking is adaptive: the thinking field is left unset and effort is the model's documented default. Raise effort for planning. Add your Anthropic API key.",
  claudeOfficial: "Model page",
  vendor: "Provider",
  chooseVendor: "Choose a provider…",
  vendorAnthropic: "Anthropic (Claude)",
  vendorOpenai: "OpenAI",
  vendorZai: "Z.ai (GLM)",
  vendorVercel: "Vercel AI Gateway (one key, many models)",
  vendorBedrock: "Amazon Bedrock (one key, many models)",
  region: "AWS Region",
  regionHelp: "Must match the Region the Bedrock API key was created in. Other Regions: edit the Base URL below.",
  getKey: "Get a {p0} API key",
  keyBedrock: "Bedrock API key",
  keyVercel: "AI Gateway API key",
  gatewayGroup: "Gateways (one key, many models)",
  vercelChat: "Vercel AI Gateway · any model, OpenAI Chat Completions",
  vercelClaude: "Vercel AI Gateway · Claude, Anthropic Messages",
  bedrockClaude: "Amazon Bedrock · Claude, Anthropic Messages",
  bedrockOpen: "Amazon Bedrock · DeepSeek, Qwen, GLM and other models",
  vercelHelp:
    "Use an AI Gateway API key from the Vercel dashboard (AI Gateway, API keys). Model IDs are provider/model, for example anthropic/claude-sonnet-5; use Load models for the exact list of providers and models. For OpenAI models that need function calling, switch Format to OpenAI (Responses API) and keep the same Base URL. Thinking, effort and the 200K window are left conservative: set them for the model you pick.",
  bedrockClaudeHelp:
    "Use an Amazon Bedrock API key (Bedrock console, API keys). The Region in the Base URL must match the Region the key was created in; change us-east-1 if needed. Model IDs have no slash, for example us.anthropic.claude-sonnet-5 (cross-Region inference profile). Load models is unavailable on bedrock-runtime: type the ID from the model's card. Short-term keys expire within 12 hours.",
  bedrockOpenHelp:
    "Use an Amazon Bedrock API key (Bedrock console, API keys). This preset uses the bedrock-mantle endpoint so Load models works and lists DeepSeek, Qwen, GLM, Kimi, Mistral and gpt-oss models. The Region in the Base URL must match the key's Region. If a model answers 'isn't supported on this route', change /v1 to /openai/v1 in the Base URL.",
  vercelDocs: "AI Gateway docs",
  bedrockDocs: "Bedrock API keys",
  warnBedrockRegion:
    "The Base URL still has a region placeholder. Replace it with your AWS Region, for example us-east-1, matching the Region your Bedrock API key was created in.",
  warnVercelAnthropicPath:
    "For Vercel AI Gateway with the Anthropic format, the Base URL must be https://ai-gateway.vercel.sh with no path (a trailing /v1 would become /v1/v1/messages).",
  warnVercelOpenAIPath:
    "For Vercel AI Gateway with an OpenAI format, the Base URL must be https://ai-gateway.vercel.sh/v1.",
  warnVercelModel:
    "Vercel AI Gateway model IDs use provider/model, for example anthropic/claude-sonnet-5. Use Load models for the exact IDs.",
  warnBedrockAnthropicPath:
    "For Amazon Bedrock with the Anthropic format, the Base URL path must be exactly /anthropic.",
  warnBedrockOpenAIPath:
    "For Amazon Bedrock bedrock-runtime with an OpenAI format, the Base URL path must be exactly /openai/v1.",
  warnMantleOpenAIPath:
    "For Amazon Bedrock bedrock-mantle with an OpenAI format, the Base URL path must be /v1 (or /openai/v1 for the few models that use it).",
  warnBedrockModel:
    "Amazon Bedrock model IDs have no slash, for example us.anthropic.claude-sonnet-5. A provider/model ID belongs to Vercel AI Gateway.",
  cwUnset:
    "Context window is not set, so Counterweight assumes 200K and compresses history early. Declare the model's real window, for example 1000 for 1M tokens.",
};

export const es = {
  subscriptionModelPlaceholder: "Elija un modelo tras iniciar sesión",
  subscriptionModelsLoading: "Cargando modelos para esta cuenta de ChatGPT…",
  subscriptionModelsHelp:
    "Los modelos provienen del catálogo nativo de Codex. Una prueba de conexión exitosa confirma el acceso al espacio de trabajo.",
  subscriptionDaybreakHelp:
    "Usa el programa de acceso a Codex anunciado por el modelo seleccionado. Los modelos no disponibles se desactivan mientras Daybreak está activo. OpenAI verifica la aprobación del espacio de trabajo en cada solicitud.",
  authMethod: "Tipo de conexión",
  apiKeyMethod: "Clave de API",
  subscriptionMethod: "Suscripción a Codex / ChatGPT",
  connectCodex: "Conectar Codex",
  continueChatGPT: "Continuar con ChatGPT",
  subscriptionAccount: "Cuenta de ChatGPT",
  subscriptionNewAccount: "Agregar una cuenta de ChatGPT",
  subscriptionHelp:
    "Use su cuenta de ChatGPT a través del entorno de ejecución local de Codex, incluidos los espacios de trabajo Enterprise con acceso a Codex habilitado. Complete el inicio de sesión en el navegador, seleccione un modelo y guarde. La CLI de Codex debe estar instalada en el host del backend. Abra Counterweight en esa misma computadora para iniciar sesión en el navegador.",
  subscriptionWaiting: "Esperando el inicio de sesión en el navegador…",
  subscriptionCancel: "Cancelar",
  subscriptionOpenBrowser: "Reabrir inicio de sesión",
  subscriptionUsage: "Gestionar el uso de ChatGPT y el acceso a la aplicación",
  subscriptionConnected: "Suscripción a ChatGPT conectada. Elija un modelo y guarde el perfil.",
  subscriptionDisconnected: "Desconectado",
  subscriptionSignInFirst: "Conecte una cuenta de ChatGPT antes de guardar este perfil.",
  popupBlocked: "Su navegador bloqueó la ventana de inicio de sesión. Permita las ventanas emergentes para Counterweight e inténtelo de nuevo.",
  loginCancelled: "El inicio de sesión en ChatGPT se canceló o expiró.",
  template: "Plantilla de configuración (solo perfil nuevo)",
  chooseTemplate: "Elija una plantilla…",
  general: "Z.ai GLM-5.3 · API General (recomendado)",
  coding: "Z.ai GLM-5.3 · Plan de Codificación (referencia)",
  introduction:
    "Opcional: rellene previamente los campos de conexión, luego introduzca un nombre de perfil y su clave de API. Su clave, proxy y nombre se conservan. La selección no guarda ni activa un perfil. Guarde cuando esté listo y luego actívelo por separado. El modelo sigue siendo editable.",
  generalHelp:
    "Usa el endpoint de la API General y la facturación por API, separada de la cuota de suscripción del Plan de Codificación. Confirme el acceso al modelo y la facturación de su cuenta de Z.ai.",
  codingHelp:
    "Solo como referencia: el Plan de Codificación usa un endpoint y una cuota de suscripción distintos. Z.ai lo restringe a herramientas con soporte oficial; Counterweight no está en la lista. Obtenga la autorización de Z.ai antes de usar este endpoint con Counterweight.",
  modelHelp:
    "GLM-5.3 es solo de texto con un contexto de 1 M de tokens. El razonamiento debe permanecer activado; los niveles de esfuerzo admitidos son bajo, alto y máximo. Esta plantilla selecciona máximo y deja el límite de salida en 0 (valor predeterminado del proveedor). Revise estos ajustes si cambia de modelo.",
  officialModel: "Guía del modelo GLM-5.3",
  officialPolicy: "Política de uso del Plan de Codificación",
  claudeGroup: "Anthropic Claude",
  zaiGroup: "Z.ai GLM",
  openaiGroup: "OpenAI",
  openaiSol: "GPT-6.1 Sol · planificador o trabajadores",
  openaiLuna: "GPT-6 Luna · trabajadores, agentes ligeros y el juez de interceptación",
  openaiHelp:
    "Usa la API de Responses, que Counterweight necesita aquí: GPT-6.1 Sol no admite llamadas a funciones en Chat Completions. La URL base queda vacía para el endpoint oficial. La ventana es de 1,05 M de tokens con 128 K de salida, por lo que la Ventana de contexto se fija en 922 (la parte de entrada). El esfuerzo es el predeterminado documentado, medio. Añada su clave de API de OpenAI.",
  openaiOfficial: "Página del modelo",
  claudeFable: "Claude Fable 5.1 · planificador para las tareas más difíciles",
  claudeOpus: "Claude Opus 5.5 · planificador y agente principal (recomendado)",
  claudeSonnet: "Claude Sonnet 5.5 · trabajadores",
  claudeHaiku: "Claude Haiku 5.5 · juez de interceptación y agentes ligeros",
  claudeHelp:
    "Usa el endpoint oficial de Anthropic, por lo que la URL base queda vacía. El modelo tiene un contexto de 1 M de tokens y 128 K de salida, por lo que la Ventana de contexto se fija en 1000. El pensamiento es adaptativo: el campo de pensamiento se deja sin definir y el esfuerzo es el predeterminado documentado del modelo. Aumente el esfuerzo para la planificación. Añada su clave de API de Anthropic.",
  claudeOfficial: "Página del modelo",
  vendor: "Proveedor",
  chooseVendor: "Elija un proveedor…",
  vendorAnthropic: "Anthropic (Claude)",
  vendorOpenai: "OpenAI",
  vendorZai: "Z.ai (GLM)",
  vendorVercel: "Vercel AI Gateway (una clave, muchos modelos)",
  vendorBedrock: "Amazon Bedrock (una clave, muchos modelos)",
  region: "Región de AWS",
  regionHelp: "Debe coincidir con la Región en la que se creó la clave de API de Bedrock. Para otras Regiones: edite la URL base a continuación.",
  getKey: "Obtener una clave de API de {p0}",
  keyBedrock: "Clave de API de Bedrock",
  keyVercel: "Clave de API de AI Gateway",
  gatewayGroup: "Gateways (una clave, muchos modelos)",
  vercelChat: "Vercel AI Gateway · cualquier modelo, OpenAI Chat Completions",
  vercelClaude: "Vercel AI Gateway · Claude, Anthropic Messages",
  bedrockClaude: "Amazon Bedrock · Claude, Anthropic Messages",
  bedrockOpen: "Amazon Bedrock · DeepSeek, Qwen, GLM y otros modelos",
  vercelHelp:
    "Use una clave de API de AI Gateway del panel de Vercel (AI Gateway, claves de API). Los ID de modelo son proveedor/modelo, por ejemplo anthropic/claude-sonnet-5; use Cargar modelos para la lista exacta de proveedores y modelos. Para los modelos de OpenAI que necesitan llamadas a funciones, cambie el Formato a OpenAI (Responses API) y mantenga la misma URL base. El pensamiento, el esfuerzo y la ventana de 200 K se dejan conservadores: configúrelos para el modelo que elija.",
  bedrockClaudeHelp:
    "Use una clave de API de Amazon Bedrock (consola de Bedrock, claves de API). La Región en la URL base debe coincidir con la Región en la que se creó la clave; cambie us-east-1 si es necesario. Los ID de modelo no llevan barra, por ejemplo us.anthropic.claude-sonnet-5 (perfil de inferencia entre Regiones). Cargar modelos no está disponible en bedrock-runtime: escriba el ID de la ficha del modelo. Las claves de corto plazo expiran en un plazo de 12 horas.",
  bedrockOpenHelp:
    "Use una clave de API de Amazon Bedrock (consola de Bedrock, claves de API). Este preajuste usa el endpoint bedrock-mantle, por lo que Cargar modelos funciona y lista los modelos DeepSeek, Qwen, GLM, Kimi, Mistral y gpt-oss. La Región en la URL base debe coincidir con la Región de la clave. Si un modelo responde «no es compatible con esta ruta», cambie /v1 por /openai/v1 en la URL base.",
  vercelDocs: "Documentación de AI Gateway",
  bedrockDocs: "Claves de API de Bedrock",
  warnBedrockRegion:
    "La URL base todavía tiene un marcador de posición de región. Reemplácelo con su Región de AWS, por ejemplo us-east-1, que coincida con la Región en la que se creó su clave de API de Bedrock.",
  warnVercelAnthropicPath:
    "Para Vercel AI Gateway con el formato Anthropic, la URL base debe ser https://ai-gateway.vercel.sh sin ruta (un /v1 final se convertiría en /v1/v1/messages).",
  warnVercelOpenAIPath:
    "Para Vercel AI Gateway con un formato OpenAI, la URL base debe ser https://ai-gateway.vercel.sh/v1.",
  warnVercelModel:
    "Los ID de modelo de Vercel AI Gateway usan proveedor/modelo, por ejemplo anthropic/claude-sonnet-5. Use Cargar modelos para los ID exactos.",
  warnBedrockAnthropicPath:
    "Para Amazon Bedrock con el formato Anthropic, la ruta de la URL base debe ser exactamente /anthropic.",
  warnBedrockOpenAIPath:
    "Para Amazon Bedrock bedrock-runtime con un formato OpenAI, la ruta de la URL base debe ser exactamente /openai/v1.",
  warnMantleOpenAIPath:
    "Para Amazon Bedrock bedrock-mantle con un formato OpenAI, la ruta de la URL base debe ser /v1 (o /openai/v1 para los pocos modelos que lo usan).",
  warnBedrockModel:
    "Los ID de modelo de Amazon Bedrock no llevan barra, por ejemplo us.anthropic.claude-sonnet-5. Un ID proveedor/modelo pertenece a Vercel AI Gateway.",
  cwUnset:
    "La Ventana de contexto no está configurada, por lo que Counterweight asume 200 K y comprime el historial pronto. Declare la ventana real del modelo, por ejemplo 1000 para 1 M de tokens.",
};
