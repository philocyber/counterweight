// Complete paragraphs keep word order and emphasis natural.
export const en = {
  captureDisabled:
    "When disabled (the default), no traffic is recorded. Agents receive <b>neither proxy settings nor traffic tools</b>, and their prompts contain <b>no proxy instructions</b>. Switching rebuilds Agents immediately to apply the change.",
  binding:
    "Off by default. When enabled, the report Agent triggered on vulnerability creation reviews existing HTTP requests and responses, links the relevant traffic, then writes the report. <b>Inspecting packets and making extra tool calls increases token usage.</b>",
  proxy:
    "All Agents’ <b>target traffic</b> uses this outbound proxy to hide the source IP or use a jump host. Supports <b>HTTP, HTTPS, and SOCKS5</b>, optionally with <code>user:pass</code> authentication. Leave blank for a direct connection.",
  proxyCapture:
    "With <b>traffic capture enabled</b>, this is the recording proxy’s <b>upstream</b>: traffic is recorded before forwarding. With capture disabled, the proxy is injected directly into Agent Bash/WebFetch. It is independent of web search and LLM proxies.",
  proxySocks:
    "<b>SOCKS5 note:</b> With capture disabled, each command-line tool must support <code>ALL_PROXY</code>. curl does; some tools may ignore it. For SOCKS5, enabling capture is recommended: the MITM proxy establishes the connection directly, without requiring tool support.",
  constraints:
    "When enabled, each task’s <b>operational constraints</b>—the allow/deny entries in Task overview—are included in the Agent’s system prompt to define exploration boundaries, such as testing only the current port or prohibiting brute force.",
  constraintTargets:
    "Control injection separately for the <b>planner</b> and <b>worker</b>; both are enabled by default. Changes apply on the next read without rebuilding Agents. When injection is disabled, that Agent no longer sees the constraints.",
  compression:
    "<b>noa context compression</b> uses the model to compress long conversation histories (norma v0.4.0). When enabled, <b>planner, worker, main-Agent, and chat</b> contexts use noa instead of built-in compression. Original content is archived in the task working directory for review. Changes affect subsequent runs without rebuilding Agents; disabling immediately restores built-in compression.",
  webSearch:
    "This is the web search <b>master switch and source configuration</b>. Enable it first, then enable <b>web_search in each Agent’s settings</b>. Search returns titles, links, and snippets; WebFetch retrieves full content. Web search <b>does not use the recording proxy</b> and is independent of traffic capture.",
  searchSources:
    "Available sources are <b>DuckDuckGo (ddgs)</b> without an API key, <b>Brave’s free tier</b> with a Brave API key, <b>Tavily</b> with a Tavily API key, or <b>DeepSeek</b> using the current LLM profile. The master switch must be on before Agents can enable web search.",
  deepseekRequirements:
    "This source reuses the <b>currently active LLM profile</b>. It supports <b>only official DeepSeek models</b>, and the profile <b>must use the Anthropic protocol</b>. DeepSeek’s OpenAI endpoint does not support server-side search. Switching LLM profiles may make this source unavailable.",
  deepseekBehavior:
    "Unlike other sources, search runs <b>on DeepSeek’s servers</b>. Each search adds a model call and token cost. Search requests <b>bypass the outbound proxy above</b> and <b>are not recorded as traffic</b>. Results contain <b>only titles and links</b>, without snippets; use WebFetch for full content.",
  python:
    "Custom <b>script</b> tools use this Python interpreter. It is detected at startup, preferring python3. Enter an absolute path to a virtual environment or specific version, or leave blank for automatic runtime detection.",
  workers:
    "Number of Worker Agents running concurrently per task (default: 3). More Workers increase parallel exploration and cost. Changes <b>apply to tasks started afterward</b>; running tasks are unaffected.",
  sendPreference:
    "This preference is stored <b>only in this browser</b> and does not sync with your account. Set it again after changing browsers or clearing site data.",
  updateLauncher:
    "One-click updates rely on the supervisor script to restart the application. Start with <code>start.sh</code> (Windows: <code>start.bat</code>). Running the binary directly does not restart it automatically after exit.",
  pagination: "{from}–{to} / {total} records",
};

export const es = {
  captureDisabled:
    "Cuando está desactivado (el valor predeterminado), no se registra tráfico. Los Agentes no reciben <b>ni configuración de proxy ni herramientas de tráfico</b>, y sus prompts no contienen <b>instrucciones de proxy</b>. Al cambiarlo, los Agentes se reconstruyen de inmediato para aplicar el cambio.",
  binding:
    "Desactivado de forma predeterminada. Cuando está activado, el Agente de informes que se activa al crear una vulnerabilidad revisa las solicitudes y respuestas HTTP existentes, vincula el tráfico relevante y luego redacta el informe. <b>Inspeccionar paquetes y hacer llamadas adicionales a herramientas aumenta el uso de tokens.</b>",
  proxy:
    "Todo el <b>tráfico de destino</b> de los Agentes usa este proxy de salida para ocultar la IP de origen o usar un host de salto. Admite <b>HTTP, HTTPS y SOCKS5</b>, opcionalmente con autenticación <code>user:pass</code>. Déjelo en blanco para una conexión directa.",
  proxyCapture:
    "Con la <b>captura de tráfico activada</b>, este es el <b>upstream</b> del proxy de grabación: el tráfico se registra antes de reenviarse. Con la captura desactivada, el proxy se inyecta directamente en Bash/WebFetch del Agente. Es independiente de los proxies de búsqueda web y de LLM.",
  proxySocks:
    "<b>Nota sobre SOCKS5:</b> con la captura desactivada, cada herramienta de línea de comandos debe admitir <code>ALL_PROXY</code>. curl lo hace; algunas herramientas pueden ignorarlo. Para SOCKS5 se recomienda activar la captura: el proxy MITM establece la conexión directamente, sin requerir soporte de la herramienta.",
  constraints:
    "Cuando está activado, las <b>restricciones operativas</b> de cada tarea (las entradas de permitir/denegar en la vista general de la tarea) se incluyen en el prompt de sistema del Agente para definir los límites de exploración, como probar solo el puerto actual o prohibir la fuerza bruta.",
  constraintTargets:
    "Controle la inyección por separado para el <b>planificador</b> y el <b>trabajador</b>; ambos están activados de forma predeterminada. Los cambios se aplican en la siguiente lectura sin reconstruir los Agentes. Cuando la inyección está desactivada, ese Agente deja de ver las restricciones.",
  compression:
    "La <b>compresión de contexto de noa</b> usa el modelo para comprimir historiales de conversación largos (norma v0.4.0). Cuando está activada, los contextos del <b>planificador, el trabajador, el Agente principal y el chat</b> usan noa en lugar de la compresión integrada. El contenido original se archiva en el directorio de trabajo de la tarea para su revisión. Los cambios afectan a las ejecuciones posteriores sin reconstruir los Agentes; desactivarla restaura de inmediato la compresión integrada.",
  webSearch:
    "Este es el <b>interruptor principal y la configuración de fuentes</b> de la búsqueda web. Actívelo primero y luego active <b>web_search en la configuración de cada Agente</b>. La búsqueda devuelve títulos, enlaces y fragmentos; WebFetch recupera el contenido completo. La búsqueda web <b>no usa el proxy de grabación</b> y es independiente de la captura de tráfico.",
  searchSources:
    "Las fuentes disponibles son <b>DuckDuckGo (ddgs)</b> sin clave de API, el <b>nivel gratuito de Brave</b> con una clave de API de Brave, <b>Tavily</b> con una clave de API de Tavily, o <b>DeepSeek</b> usando el perfil de LLM actual. El interruptor principal debe estar activado antes de que los Agentes puedan habilitar la búsqueda web.",
  deepseekRequirements:
    "Esta fuente reutiliza el <b>perfil de LLM activo actualmente</b>. Solo admite <b>modelos oficiales de DeepSeek</b>, y el perfil <b>debe usar el protocolo Anthropic</b>. El endpoint OpenAI de DeepSeek no admite búsqueda del lado del servidor. Cambiar de perfil de LLM puede dejar esta fuente no disponible.",
  deepseekBehavior:
    "A diferencia de otras fuentes, la búsqueda se ejecuta <b>en los servidores de DeepSeek</b>. Cada búsqueda añade una llamada al modelo y un costo de tokens. Las solicitudes de búsqueda <b>omiten el proxy de salida anterior</b> y <b>no se registran como tráfico</b>. Los resultados contienen <b>solo títulos y enlaces</b>, sin fragmentos; use WebFetch para el contenido completo.",
  python:
    "Las herramientas de <b>script</b> personalizadas usan este intérprete de Python. Se detecta al inicio, prefiriendo python3. Introduzca una ruta absoluta a un entorno virtual o a una versión específica, o déjelo en blanco para la detección automática del entorno de ejecución.",
  workers:
    "Número de Agentes trabajadores que se ejecutan en paralelo por tarea (predeterminado: 3). Más trabajadores aumentan la exploración en paralelo y el costo. Los cambios <b>se aplican a las tareas iniciadas después</b>; las tareas en ejecución no se ven afectadas.",
  sendPreference:
    "Esta preferencia se almacena <b>solo en este navegador</b> y no se sincroniza con su cuenta. Vuelva a configurarla tras cambiar de navegador o borrar los datos del sitio.",
  updateLauncher:
    "Las actualizaciones con un clic dependen del script supervisor para reiniciar la aplicación. Inicie con <code>start.sh</code> (en Windows: <code>start.bat</code>). Ejecutar el binario directamente no lo reinicia automáticamente tras salir.",
  pagination: "{from}–{to} / {total} registros",
};
