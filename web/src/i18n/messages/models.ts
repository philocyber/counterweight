// models messages. Guided "Connect a model" flow on the Models page. English is the source catalog; add the Spanish string for every key.

export const en = {
  // Header, list and empty state
  connectCta: "Connect a model",
  activeBadge: "Active",
  emptyProfiles: "No model connected yet. Agents need one to work.",

  // Drawer
  connectTitle: "Connect a model",
  connectDescription: "Choose where the model runs, enter what that option needs, test it, then save.",
  chooseAnotherWay: "Choose another way to connect",
  stepLabel: "Step {p0} of {p1}",
  stepWhere: "Where the model runs",
  stepDetails: "Connection details",
  stepTest: "Test and save",
  back: "Back",
  continue: "Continue",

  // Step 1: where the model runs
  dataNote:
    "Agents send everything they read to the model: source code, responses from your targets and command output. Pick an option your organization may send that to.",
  groupHosted: "Hosted API",
  groupGateway: "Gateway",
  groupLocal: "Local or self-hosted",
  groupSubscription: "ChatGPT / Codex sign-in",
  choiceAnthropic: "Anthropic",
  choiceOpenai: "OpenAI",
  choiceZai: "Z.ai",
  choiceVercel: "Vercel AI Gateway",
  choiceBedrock: "Amazon Bedrock",
  choiceOpencode: "OpenCode Zen / Go",
  choiceLocal: "Any OpenAI-compatible endpoint",
  choiceLocalHint: "Ollama, LM Studio, llama.cpp, vLLM",
  choiceCodex: "Sign in with ChatGPT",
  choiceCodexHint: "Use your ChatGPT subscription through Codex. No API key needed.",
  leavesHosted: "Prompts and target data are sent to {p0}.",
  leavesGateway: "Prompts and target data are sent to {p0}, which passes them on to the model provider.",
  leavesLocal:
    "Nothing leaves your machine. A server run by someone else, such as a rented GPU, still receives your data.",
  leavesCodex: "Prompts and target data are sent to OpenAI under your workspace's terms.",

  // Step 2: connection details
  serverAddress: "Server address (base URL)",
  serverAddressHelp: "The address of the server, ending in /v1. Pick a common default or type your own.",
  dockerNote:
    "If Counterweight runs in Docker, localhost points at the container itself. Use host.docker.internal instead, for example http://host.docker.internal:11434/v1.",
  localKeyHelp:
    "A placeholder key is filled in. Local servers ignore it, but Counterweight requires a non-empty key. Replace it only if your server checks one.",
  localModelPlaceholder: "Model name as the server lists it",
  loadModels: "Load models",
  modelHelp: "Load models asks the provider which models your key can use. If that fails, type the model name.",
  detailsIncomplete: "Enter a name, an API key and a model to continue.",
  detailsIncompleteLocal: "Enter a name, a server address, a key and a model to continue.",

  // Step 3: test and save
  testHelp:
    "Sends one short request with these settings, so a wrong key, address or model shows up now and not in the middle of a task.",
  testStale: "Settings changed since the last test. Test again.",
  skipTest: "Save without testing",
  skipTestHelp: "Saves a profile that may not work. You can test it later by opening it from the list.",
  saveProfile: "Save model",
  advancedTitle: "Advanced settings",
  advancedHelp:
    "Format, proxy, reasoning, limits, streaming and rotation. Prefilled for this choice; most people leave them as they are.",
  localContextHelp:
    "The context size you configured on the server, minus the output limit. Agents need 128K tokens or more, and the default of some servers (Ollama) is far lower, so raise it on the server first.",
  localOutputHelp:
    "Some servers reserve the whole window for the reply when no limit is sent, then reject the request. 8192 is a safe start.",

  // After saving
  savedTitle: "Saved: {p0}",
  savedActivated: "It is now your active model, because none was active yet.",
  savedActivationFailed: "It was saved but could not be made active. Use Set active on its card.",
  makeActiveQuestion: "Make this the active model?",
  makeActiveHelp: "Agents will use it right away. If you leave this off, your current active model stays active.",
  done: "Done",
  healthUnmonitored: "Not monitored",
  healthUnmonitoredHint:
    "Failures are tracked only while this profile is in an active rotation. Use Test connection to check it now.",
};

export const es: Partial<Record<keyof typeof en, string>> = {
  connectCta: "Conectar un modelo",
  activeBadge: "Activo",
  emptyProfiles: "Aún no hay ningún modelo conectado. Los agentes necesitan uno para trabajar.",

  connectTitle: "Conectar un modelo",
  connectDescription: "Elige dónde se ejecuta el modelo, completa lo que esa opción necesita, pruébalo y guárdalo.",
  chooseAnotherWay: "Elegir otra forma de conectar",
  stepLabel: "Paso {p0} de {p1}",
  stepWhere: "Dónde se ejecuta el modelo",
  stepDetails: "Datos de conexión",
  stepTest: "Probar y guardar",
  back: "Atrás",
  continue: "Continuar",

  dataNote:
    "Los agentes envían al modelo todo lo que leen: código fuente, respuestas de tus objetivos y salida de comandos. Elige una opción a la que tu organización pueda enviar eso.",
  groupHosted: "API alojada",
  groupGateway: "Pasarela",
  groupLocal: "Local o autoalojado",
  groupSubscription: "Inicio de sesión con ChatGPT / Codex",
  choiceAnthropic: "Anthropic",
  choiceOpenai: "OpenAI",
  choiceZai: "Z.ai",
  choiceVercel: "Vercel AI Gateway",
  choiceBedrock: "Amazon Bedrock",
  choiceOpencode: "OpenCode Zen / Go",
  choiceLocal: "Cualquier endpoint compatible con OpenAI",
  choiceLocalHint: "Ollama, LM Studio, llama.cpp, vLLM",
  choiceCodex: "Iniciar sesión con ChatGPT",
  choiceCodexHint: "Usa tu suscripción de ChatGPT a través de Codex. No necesita clave de API.",
  leavesHosted: "Los prompts y los datos de los objetivos se envían a {p0}.",
  leavesGateway: "Los prompts y los datos de los objetivos se envían a {p0}, que los reenvía al proveedor del modelo.",
  leavesLocal:
    "Nada sale de tu equipo. Un servidor administrado por otra persona, como una GPU alquilada, sí recibe tus datos.",
  leavesCodex:
    "Los prompts y los datos de los objetivos se envían a OpenAI bajo los términos de tu espacio de trabajo.",

  serverAddress: "Dirección del servidor (URL base)",
  serverAddressHelp: "La dirección del servidor, terminada en /v1. Elige una opción habitual o escribe la tuya.",
  dockerNote:
    "Si Counterweight se ejecuta en Docker, localhost apunta al propio contenedor. Usa host.docker.internal en su lugar, por ejemplo http://host.docker.internal:11434/v1.",
  localKeyHelp:
    "Se rellena una clave de relleno. Los servidores locales la ignoran, pero Counterweight exige una clave no vacía. Cámbiala solo si tu servidor valida una.",
  localModelPlaceholder: "Nombre del modelo como lo lista el servidor",
  loadModels: "Cargar modelos",
  modelHelp:
    "Cargar modelos le pregunta al proveedor qué modelos puede usar tu clave. Si falla, escribe el nombre del modelo.",
  detailsIncomplete: "Ingresa un nombre, una clave de API y un modelo para continuar.",
  detailsIncompleteLocal: "Ingresa un nombre, una dirección de servidor, una clave y un modelo para continuar.",

  testHelp:
    "Envía una solicitud corta con estos ajustes, para que una clave, dirección o modelo incorrectos aparezcan ahora y no en medio de una tarea.",
  testStale: "Los ajustes cambiaron desde la última prueba. Vuelve a probar.",
  skipTest: "Guardar sin probar",
  skipTestHelp: "Guarda un perfil que quizá no funcione. Puedes probarlo después abriéndolo desde la lista.",
  saveProfile: "Guardar modelo",
  advancedTitle: "Ajustes avanzados",
  advancedHelp:
    "Formato, proxy, razonamiento, límites, streaming y rotación. Ya vienen rellenados para esta opción; la mayoría los deja como están.",
  localContextHelp:
    "El tamaño de contexto que configuraste en el servidor, menos el límite de salida. Los agentes necesitan 128K tokens o más, y el valor por defecto de algunos servidores (Ollama) es mucho menor, así que súbelo primero en el servidor.",
  localOutputHelp:
    "Algunos servidores reservan toda la ventana para la respuesta cuando no se envía un límite y luego rechazan la solicitud. 8192 es un buen punto de partida.",

  savedTitle: "Guardado: {p0}",
  savedActivated: "Ahora es tu modelo activo, porque todavía no había ninguno activo.",
  savedActivationFailed: "Se guardó pero no se pudo activar. Usa Establecer activo en su tarjeta.",
  makeActiveQuestion: "¿Usar este como modelo activo?",
  makeActiveHelp: "Los agentes lo usarán de inmediato. Si lo dejas desactivado, tu modelo activo actual sigue activo.",
  done: "Listo",
  healthUnmonitored: "Sin monitoreo",
  healthUnmonitoredHint:
    "Las fallas solo se registran mientras este perfil está en una rotación activa. Usa Probar conexión para verificarlo ahora.",
};
