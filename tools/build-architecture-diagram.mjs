#!/usr/bin/env node
// Writes the architecture diagram as static SVG files, one per language and colour scheme:
//   docs/brand/architecture-{en,es}-{light,dark}.svg   (README.md, README.es.md)
//   web/public/diagrams/architecture-{light,dark}.svg  (in-app docs, English)
// Usage: node tools/build-architecture-diagram.mjs
//        node tools/build-architecture-diagram.mjs --check <dir>   (copies with data-fit widths, for a layout check)
// Edit the text in STRINGS or the layout below, run the script, and commit the four files.
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");

// Colours follow the console theme (web/src/app/globals.css).
const THEMES = {
  light: {
    canvas: "#f5f3ec", panel: "#fbfaf6", box: "#ffffff", engine: "#e6f1ef", store: "#ecebe3",
    border: "#dcd8cb", text: "#24363d", muted: "#5a6b70", primary: "#158a88", onPrimary: "#fbfaf6",
    onPrimaryMuted: "#d6efec", logoMain: "#24363d",
  },
  dark: {
    canvas: "#0f1416", panel: "#171e21", box: "#1e2529", engine: "#132826", store: "#12181b",
    border: "#2b363b", text: "#e8eceb", muted: "#9aa7ab", primary: "#53c5be", onPrimary: "#062523",
    onPrimaryMuted: "#0e3a37", logoMain: "#e6ecea",
  },
};

const STRINGS = {
  en: {
    title: "Counterweight architecture",
    desc: "An operator, a CI workflow or a coding agent reaches the Counterweight server. Inside it, the console and API hand work to the agent engine. Agents that test running applications pass through the approval gate, execution isolation and an optional recording proxy before reaching authorized targets. Agents that review source code only read it; scanner leads, an independent verifier and a fix step follow, and fixes go to GitHub as pull requests. Findings become reports and Jira tickets. Every agent calls the model provider you connect. Data stays in PostgreSQL, the data directory and a separate key directory.",
    entries: [
      ["Operator", "browser console over HTTPS"],
      ["CI workflow", "GitHub Action, scoped API token"],
      ["Coding agents", "Claude Code, Codex, Pi adapter"],
    ],
    legend: ["a person decides", "optional"],
    server: "Counterweight server",
    serverSub: "one binary · self-hosted",
    console: ["Console and API", "Embedded web console · revocable sessions · credentials encrypted at rest"],
    engine: "AGENT ENGINE",
    lane1: "Running applications and networks",
    lane1Boxes: [
      ["Planner + workers", "shared asset and", "exploration graphs"],
      ["Approval gate", "scope, deny rules,", "a person decides"],
      ["Isolation", "confined files or a", "container per task"],
      ["Recording proxy", "traffic evidence,", "port 8788"],
    ],
    lane2: "Source code",
    lane2Boxes: [
      ["Review agents", "read only: no shell,", "no network"],
      ["Scanner leads", "OSV-Scanner, Gitleaks,", "after the review"],
      ["Verifier", "tries to refute", "each finding"],
      ["Fix and re-check", "patch review, pull", "request, re-check"],
    ],
    outputs: [
      ["Findings and evidence", "verified, retested, tracked"],
      ["Reports", "Markdown · PDF · SARIF"],
      ["Jira hand-off", "priorities and due dates"],
    ],
    outside: "OUTSIDE THE SERVER",
    externals: [
      ["Your model provider", "hosted, gateway or local"],
      ["Authorized targets", "only what is in scope"],
      ["GitHub", "clone, pull requests"],
      ["Jira Cloud", "epic per task, issue per finding"],
    ],
    stores: [
      ["PostgreSQL", "tasks, findings, audit trail"],
      ["data/", "task workspaces and evidence"],
      ["Keys", "kept outside the data directory"],
    ],
  },
  es: {
    title: "Arquitectura de Counterweight",
    desc: "Un operador, un workflow de CI o un agente de código llegan al servidor de Counterweight. Dentro, la consola y la API entregan el trabajo al motor de agentes. Los agentes que prueban aplicaciones en ejecución pasan por la compuerta de aprobación, el aislamiento de ejecución y un proxy de grabación opcional antes de llegar a los objetivos autorizados. Los agentes que revisan código fuente solo lo leen; después vienen las pistas de los escáneres, un verificador independiente y la corrección, que llega a GitHub como pull request. Los hallazgos se convierten en informes y tickets de Jira. Cada agente llama al proveedor de modelos que conectes. Los datos quedan en PostgreSQL, el directorio de datos y un directorio de claves separado.",
    entries: [
      ["Operador", "consola en el navegador, HTTPS"],
      ["Workflow de CI", "GitHub Action, token de API"],
      ["Agentes de código", "Claude Code, Codex, Pi"],
    ],
    legend: ["decide una persona", "opcional"],
    server: "Servidor de Counterweight",
    serverSub: "un binario · autoalojado",
    console: ["Consola y API", "Consola web integrada · sesiones revocables · credenciales cifradas en reposo"],
    engine: "MOTOR DE AGENTES",
    lane1: "Aplicaciones y redes en ejecución",
    lane1Boxes: [
      ["Planner + workers", "grafos compartidos de", "activos y exploración"],
      ["Aprobación", "alcance, reglas,", "decide una persona"],
      ["Aislamiento", "archivos confinados o", "contenedor por tarea"],
      ["Proxy de grabación", "evidencia de tráfico,", "puerto 8788"],
    ],
    lane2: "Código fuente",
    lane2Boxes: [
      ["Agentes de revisión", "solo lectura: sin", "shell ni red"],
      ["Escáneres", "OSV-Scanner, Gitleaks,", "tras la revisión"],
      ["Verificador", "intenta refutar", "cada hallazgo"],
      ["Corrección", "revisión del parche,", "pull request"],
    ],
    outputs: [
      ["Hallazgos y evidencia", "verificados y con seguimiento"],
      ["Informes", "Markdown · PDF · SARIF"],
      ["Entrega a Jira", "prioridades y vencimientos"],
    ],
    outside: "FUERA DEL SERVIDOR",
    externals: [
      ["Tu proveedor de modelos", "alojado, gateway o local"],
      ["Objetivos autorizados", "solo lo que está en alcance"],
      ["GitHub", "clonado, pull requests"],
      ["Jira Cloud", "épica por tarea, issue por hallazgo"],
    ],
    stores: [
      ["PostgreSQL", "tareas, hallazgos, auditoría"],
      ["data/", "archivos de tarea y evidencia"],
      ["Claves", "fuera del directorio de datos"],
    ],
  },
};

const FONT = "ui-sans-serif, system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif";
const esc = (s) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");

function build(lang, scheme, check = false) {
  const c = THEMES[scheme];
  const s = STRINGS[lang];
  const out = [];
  const W = 1000;
  const H = 680;

  // fit marks the width a text may use; it is only written for --check.
  const fit = (w) => (check ? ` data-fit="${w}"` : "");
  const box = (x, y, w, h, title, lines, o = {}) => {
    const fill = o.solid ? c.primary : (o.fill ?? c.box);
    const stroke = o.solid ? c.primary : c.border;
    const dash = o.optional ? ` stroke-dasharray="5 4"` : "";
    const titleFill = o.solid ? c.onPrimary : c.text;
    const subFill = o.solid ? c.onPrimaryMuted : c.muted;
    const tx = o.center ? x + w / 2 : x + 12;
    const anchor = o.center ? ` text-anchor="middle"` : "";
    const lineHeight = 15;
    const block = 16 + lines.length * lineHeight;
    const top = y + (h - block) / 2 + 13;
    out.push(`<rect x="${x}" y="${y}" width="${w}" height="${h}" rx="9" fill="${fill}" stroke="${stroke}" stroke-width="1.2"${dash}/>`);
    out.push(`<text x="${tx}" y="${top}" font-size="13.5" font-weight="600" fill="${titleFill}"${anchor}${fit(w - 22)}>${esc(title)}</text>`);
    lines.forEach((line, i) => {
      out.push(`<text x="${tx}" y="${top + 17 + i * lineHeight}" font-size="12" fill="${subFill}"${anchor}${fit(w - 22)}>${esc(line)}</text>`);
    });
  };
  const label = (x, y, text, o = {}) => {
    const anchor = o.end ? ` text-anchor="end"` : "";
    out.push(`<text x="${x}" y="${y}" font-size="10.5" font-weight="600" letter-spacing="1.1" fill="${c.muted}"${anchor}>${esc(text)}</text>`);
  };
  const arrow = (d, o = {}) => {
    const start = o.both ? ` marker-start="url(#tail)"` : "";
    out.push(`<path d="${d}" fill="none" stroke="${c.primary}" stroke-width="1.6" stroke-linecap="round" marker-end="url(#head)"${start}/>`);
  };
  const link = (d) => out.push(`<path d="${d}" fill="none" stroke="${c.border}" stroke-width="1.6" stroke-linecap="round" stroke-dasharray="2 5"/>`);

  out.push(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${W} ${H}" width="${W}" height="${H}" role="img" aria-labelledby="t d" font-family="${FONT}">`);
  out.push(`<title id="t">${esc(s.title)}</title>`);
  out.push(`<desc id="d">${esc(s.desc)}</desc>`);
  out.push(`<defs>`);
  out.push(`<marker id="head" viewBox="0 0 8 8" refX="7" refY="4" markerWidth="7" markerHeight="7" orient="auto"><path d="M1 1 L7 4 L1 7" fill="none" stroke="${c.primary}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></marker>`);
  out.push(`<marker id="tail" viewBox="0 0 8 8" refX="1" refY="4" markerWidth="7" markerHeight="7" orient="auto"><path d="M7 1 L1 4 L7 7" fill="none" stroke="${c.primary}" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></marker>`);
  out.push(`</defs>`);
  out.push(`<rect x="0.5" y="0.5" width="${W - 1}" height="${H - 1}" rx="16" fill="${c.canvas}" stroke="${c.border}"/>`);

  // Entry points.
  const entryX = [20, 263, 506];
  s.entries.forEach(([title, sub], i) => box(entryX[i], 20, 230, 60, title, [sub]));
  entryX.forEach((x) => arrow(`M${x + 115} 80 V106`));

  // Legend.
  out.push(`<rect x="772" y="612" width="18" height="12" rx="3" fill="${c.primary}"/>`);
  out.push(`<text x="798" y="622.5" font-size="12" fill="${c.muted}">${esc(s.legend[0])}</text>`);
  out.push(`<rect x="772" y="638" width="18" height="12" rx="3" fill="none" stroke="${c.muted}" stroke-width="1.2" stroke-dasharray="4 3"/>`);
  out.push(`<text x="798" y="648.5" font-size="12" fill="${c.muted}">${esc(s.legend[1])}</text>`);

  // Server.
  out.push(`<rect x="20" y="108" width="716" height="460" rx="14" fill="${c.panel}" stroke="${c.border}" stroke-width="1.2"/>`);
  out.push(`<g transform="translate(36 120) scale(0.0351)"><g transform="translate(-250 -280)">` +
    `<path d="M829.3 425A310 310 0 1 0 497.9 935.4" stroke="${c.logoMain}" fill="none" stroke-width="60" stroke-linecap="round"/>` +
    `<path d="M922.3 680.4A310 310 0 0 1 576.2 955.7" stroke="${c.primary}" fill="none" stroke-width="60" stroke-linecap="round"/>` +
    `<polygon points="679.2,683 604.6,812.2 455.4,812.2 380.8,683 455.4,553.8 604.6,553.8" fill="${c.logoMain}" stroke="${c.logoMain}" stroke-width="36" stroke-linejoin="round"/>` +
    `<polygon points="964.1,547 926.1,612.9 849.9,612.9 811.9,547 849.9,481.1 926.1,481.1" fill="${c.primary}" stroke="${c.primary}" stroke-width="24" stroke-linejoin="round"/></g></g>`);
  out.push(`<text x="70" y="138.5" font-size="15.5" font-weight="700" fill="${c.text}">${esc(s.server)}</text>`);
  out.push(`<text x="718" y="138.5" font-size="12" fill="${c.muted}" text-anchor="end">${esc(s.serverSub)}</text>`);

  box(38, 154, 680, 52, s.console[0], [s.console[1]]);
  arrow(`M378 206 V222`);

  // Agent engine.
  out.push(`<rect x="38" y="224" width="680" height="244" rx="11" fill="${c.engine}"/>`);
  label(52, 246, s.engine);
  const laneX = [52, 218, 384, 550];
  const lane = (y, title, boxes, style) => {
    out.push(`<text x="52" y="${y - 8}" font-size="12.5" font-weight="600" fill="${c.text}">${esc(title)}</text>`);
    boxes.forEach(([t, a, b], i) => box(laneX[i], y, 154, 72, t, [a, b], style(i)));
    for (let i = 0; i < 3; i++) arrow(`M${laneX[i] + 155.5} ${y + 36} H${laneX[i + 1] - 2}`);
  };
  lane(276, s.lane1, s.lane1Boxes, (i) => (i === 1 ? { solid: true } : i === 3 ? { optional: true } : {}));
  lane(380, s.lane2, s.lane2Boxes, () => ({}));

  // Outputs.
  arrow(`M146 468 V488`);
  const outX = [38, 270, 502];
  s.outputs.forEach(([title, sub], i) => box(outX[i], 490, 216, 60, title, [sub]));
  arrow(`M255.5 520 H268`);
  arrow(`M487.5 520 H500`);

  // Outside the server.
  label(772, 204, s.outside);
  const extY = [216, 284, 388, 492];
  s.externals.forEach(([title, sub], i) => box(772, extY[i], 208, 56, title, [sub]));
  arrow(`M719.5 244 H770`);
  arrow(`M705.5 312 H770`);
  arrow(`M705.5 416 H770`, { both: true });
  arrow(`M719.5 520 H770`);

  // Storage.
  s.stores.forEach(([title, sub], i) => {
    link(`M${entryX[i] + 115} 570 V598`);
    box(entryX[i], 600, 230, 60, title, [sub], { fill: c.store });
  });

  out.push(`</svg>`);
  return out.join("\n") + "\n";
}

const checkDir = process.argv[2] === "--check" ? process.argv[3] : "";
if (process.argv.length > 2 && !checkDir) {
  console.error("Usage: node tools/build-architecture-diagram.mjs [--check <dir>]");
  process.exit(2);
}
const outDir = checkDir ? resolve(checkDir) : join(root, "docs", "brand");
const consoleDir = join(root, "web", "public", "diagrams");
mkdirSync(outDir, { recursive: true });
const write = (file, text) => {
  writeFileSync(file, text);
  console.log("wrote", file);
};
for (const lang of Object.keys(STRINGS)) {
  for (const scheme of Object.keys(THEMES)) {
    const svg = build(lang, scheme, Boolean(checkDir));
    write(join(outDir, `architecture-${lang}-${scheme}.svg`), svg);
    if (!checkDir && lang === "en") {
      mkdirSync(consoleDir, { recursive: true });
      write(join(consoleDir, `architecture-${scheme}.svg`), svg);
    }
  }
}
