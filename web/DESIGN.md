---
name: Counterweight application console
description: A compact desktop console for inspecting agent work, scope and recorded evidence.
colors:
  light-background: "#f5f3ec"
  light-foreground: "#24363d"
  light-card: "#fbfaf6"
  light-primary: "#158a88"
  light-primary-foreground: "#fbfaf6"
  light-muted: "#ecebe3"
  light-muted-foreground: "#5a6b70"
  light-accent: "#dfeeec"
  light-accent-foreground: "#0e6765"
  light-border: "#e2dfd5"
  light-input: "#d2cfc4"
  light-ring: "#158a88"
  light-destructive: "#b42318"
  dark-background: "#0f1416"
  dark-foreground: "#e8eceb"
  dark-card: "#171e21"
  dark-popover: "#1c2327"
  dark-primary: "#53c5be"
  dark-primary-foreground: "#062523"
  dark-muted: "#1e2529"
  dark-muted-foreground: "#9aa7ab"
  dark-accent: "#15302f"
  dark-accent-foreground: "#8edcd6"
  dark-border: "#2b363b"
  dark-input: "#36424a"
  dark-destructive: "#f47575"
  topbar-background: "#1b2a30"
  topbar-foreground: "#f3f2ec"
  topbar-accent: "#53c5be"
typography:
  headline:
    fontFamily: "Instrument Sans, system-ui, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 600
    lineHeight: 1.3
    letterSpacing: "-0.025em"
  title:
    fontFamily: "Instrument Sans, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 600
    lineHeight: 1.375
  body:
    fontFamily: "Instrument Sans, system-ui, sans-serif"
    fontSize: "0.875rem"
    fontWeight: 400
    lineHeight: 1.5
  label:
    fontFamily: "Instrument Sans, system-ui, sans-serif"
    fontSize: "0.75rem"
    fontWeight: 500
    lineHeight: 1.333333
  mono:
    fontFamily: "Geist Mono, ui-monospace, monospace"
    fontWeight: 400
rounded:
  sm: "4px"
  md: "6px"
  lg: "8px"
  xl: "12px"
spacing:
  1: "4px"
  2: "8px"
  3: "12px"
  4: "16px"
  6: "24px"
components:
  button-primary:
    rounded: "{rounded.md}"
    height: "36px"
    padding: "0 12px"
  button-outline:
    rounded: "{rounded.md}"
    height: "36px"
    padding: "0 12px"
  button-ghost:
    rounded: "{rounded.md}"
    height: "36px"
    padding: "0 12px"
  input:
    rounded: "{rounded.md}"
    height: "32px"
    padding: "4px 10px"
  card:
    rounded: "{rounded.lg}"
    padding: "16px"
---

# Design System: Counterweight application console

## Overview

**Creative North Star: "The defender's counterweight"**

Counterweight is an operational research console whose layout discipline is modelled on Akto's security dashboard — a global deep-slate top bar, a grouped quiet sidebar, soft-lifted cards and tabbed table workspaces — carried over to Counterweight's own capabilities, data and tags rather than copied screen for screen. Compact controls, quiet navigation and readable evidence take priority over editorial or marketing composition. Instrument Sans supplies the interface voice; Geist Mono distinguishes identifiers and code. The logo is a broken ring balanced by a heavy neutral hexagon and a small teal one: the small weight that restores equilibrium.

This document describes the implemented default console theme at the `web/` boundary. The separate public landing page under `landing/` keeps its own styles. The console ships a single light/dark identity; the earlier selectable theme presets and alternate interface fonts were removed. The approved Chat + Auto composition belongs to the console surface contract, rather than a rule that every workspace must become a chat layout.

**Key Characteristics:**

- A persistent deep-slate global top bar (logo, centred search, Ask Counterweight, preferences, theme, account) above a grouped navigation sidebar and the workspace.
- Warm cream surfaces (never flat white) with slate ink and a mineral-teal brand accent: `#158a88` in light mode, a brighter `#53c5be` in dark mode, reserved for primary actions, active navigation, focus and links.
- Semantic severity and review states that remain distinct from the brand teal: Critical stays solid rose, High soft red, Medium amber, Low slate, always paired with a written label.
- Soft-lifted cards and tabbed table workspaces with pill filters and relative timestamps, shared across workspaces alongside the existing fields and Radix overlays.

## Colors

The neutral palette separates application ground, content surfaces and quiet metadata without decorative color fields.

### Primary

- **Mineral teal:** light-mode actions, selected navigation, caret and focus signals use `#158a88`.
- **Bright teal:** the corresponding dark-mode interaction signal is `#53c5be`, with deep slate text on filled primary controls.
- **Deep-slate top bar:** the global header stays deep slate (`#1b2a30`) in both colour modes; its controls use the dark token set and a teal status dot on the Ask Counterweight pill.
- **Tinted accent surfaces:** selected or contextual states use the paired teal accent foreground rather than replacing semantic status colors.

### Neutral

- **Cream ground and warm surface:** light-mode page background (`#f5f3ec`) and bordered content containers (`#fbfaf6`); flat white is avoided.
- **Graphite ground and depth ladder:** in dark mode the top bar and sidebar recede (`#0b0f11`), then the page canvas (`#0f1416`), raised cards (`#171e21`) and floating popovers (`#1c2327`). Neutrals keep a cool cast, not a green one.
- **Muted ink and hairlines:** supporting labels, table headers, input boundaries and dividers remain visible in both themes.

Destructive actions use the destructive token. Status colors come from the existing shared status map: Critical uses solid rose with white text, High soft red, Medium amber and Low slate. Finding review status is independent: Pending amber, In progress blue, Confirmed red, Resolved/Fixed green, False positive slate and Ignored/Duplicate neutral. Preserve the written label alongside color.

**The Semantic Color Rule.** Brand accents identify interaction; severity and review colors retain their domain meaning.

## Typography

**Interface Font:** Instrument Sans (variable, 400 to 700), with system-ui and sans-serif fallbacks; self-hosted.

**Code Font:** Geist Mono Regular and Medium, with ui-monospace and monospace fallbacks; self-hosted independently of font CDNs.

The hierarchy is intentionally shallow. Standard workspace headings use the headline role; compact cards use the title role. Chat retains its 16px heading and task detail its 14px header. Body text and ordinary controls use the body scale, while supporting labels use the label scale. Code, preformatted evidence and keyboard hints use the mono family. Code and tables use tabular numerals. Route-specific overrides remain authoritative where they carry a denser tool layout.

**The Data Type Rule.** Keep code and identifiers typographically distinct from interface prose; do not turn workspace headings into display typography.

## Layout

The application shell is a full-width graphite top bar over a row of collapsible side navigation and workspace content, following Akto's global-header pattern. The top bar carries the brand, a centred command search, the Ask Counterweight entry, update badge, preferences, theme toggle and account menu; the sidebar drops the brand and account rows it used to duplicate. Task and finding details offset their sticky headers below the top bar and no longer carry their own sidebar toggle. Repeated spacing follows a four-pixel rhythm, usually 12 or 16 pixels inside compact controls and cards, with 24-pixel desktop workspace padding where the route calls for it. Card small variants use 12-pixel spacing.

The approved Chat workspace uses a flexible conversation column and an optional task-context column bounded from 20 to 24rem. History is a toggleable 14rem rail; history and task context are mutually exclusive. The transcript scrolls within its panel and the composer remains available below it; the context panel scrolls independently. Existing Auto, model, attachment and side-question controls remain part of that working layout.

Task detail fixes its controls and wrapping workspace tabs above a separate scrolling content pane. Each selected tab mounts a fresh pane, so a long report does not leave the next tab scrolled down. Results ledger uses a flexible table and a 20rem inspector at the extra-large breakpoint; the inspector is sticky within the workspace. Preserve the original ten task tabs alongside Results ledger.

Existing responsive fallbacks and coarse-pointer control sizing remain in the implementation. Owner acceptance for this work is desktop/laptop only: 1440px light and 1280px dark captures, not a mobile acceptance claim. Breakpoints and motion are recorded in the sidecar.

## Elevation & Depth

Borders and tonal layering provide normal workspace separation. Cards have no decorative lift. Active sidebar items combine a tinted surface with a one-pixel inset primary marker. The toolbar has a faint bottom shadow. Popovers, menus, sheets and dialogs use the existing shared elevation utilities; dialogs pair that elevation with a dark translucent backdrop. Preserve these layers and Radix focus behavior rather than adding separate route-specific overlay treatments.

**The Quiet Surface Rule.** Use the existing border and surface hierarchy for workspaces; reserve stronger elevation for overlays and existing active-control states.

## Shapes

Controls use small curved corners, cards slightly larger ones, and badges a pill silhouette. The corner scale comes from the seven-pixel base radius and its derived smaller values. One-pixel boundaries establish tables and containers; a table already inside a card does not add another outer border. Lucide SVG icons remain compact and visually secondary to labels.

## Components

### Buttons

Compact, labeled controls use medium-weight interface text. Primary buttons pair the theme's primary and primary-foreground tokens. Outline buttons retain a visible boundary, secondary buttons use the muted surface family, and ghost buttons supply quiet toolbar actions. Destructive buttons use a tinted destructive surface. Default buttons are 36px tall; small, extra-small and icon variants serve dense controls. Hover changes color or tone without moving the control. Focus uses the theme ring; disabled state reduces opacity and prevents interaction.

### Inputs / Fields

Fields use the input border, compact horizontal padding and a theme-aware caret. Default inputs are 32px tall, with an additional translucent input surface in dark mode. Focus changes the border and adds a ring; invalid fields use destructive color, and disabled fields reduce contrast and block interaction. Selects and textareas share this vocabulary.

### Chips

Badges are compact pills with explicit text. Default and secondary variants carry generic emphasis; status badges use the shared severity or lifecycle mapping. Critical remains visibly stronger than High. A completed agent intent is not a passed security test.

### Cards / Containers

Cards use the card surface, a one-pixel border, the larger corner step and 16px internal spacing, or 12px for the small variant. Headers, descriptions and footer boundaries establish structure without decorative panels nested inside panels. Tables preserve clear headers, selected rows and readable wrapping for evidence summaries.

### Navigation

Sidebar rows use compact interface text, a quiet hover surface and the tinted active state with its inset marker. The icon-only collapsed state retains accessible controls. Task workspace tabs use the existing line variant with primary active underline; the task header wraps them instead of hiding original workspaces. Shared dialogs, confirmations, sheets, menus and nested selects retain keyboard behavior and visible focus.

### Evidence workspaces

Task context is explicitly an inspection selection. A successful `spawn_task` tool result may suggest the created task; free-form conversation text does not. Findings counts lead to the authoritative task-filtered registered records, while the original exploration findings remain separately labeled. Context links expose all eleven task workspaces.

Results ledger preserves original conclusions and incomplete attempts, with a searchable list and selected-record inspector. Report preview provides refresh, copy, download and retry. Markdown export retains finding review states, task-scoped counts, negative/incomplete records and explicit caps/limitations. These views must not imply exhaustive testing, a new PDF generator or universal low-impact execution enforcement.

## Do's and Don'ts

### Do:

- **Do** reuse the shared theme tokens, components and focus treatment in every workspace.
- **Do** preserve full task navigation, evidence provenance and independent review labels.
- **Do** keep scroll ownership explicit so task controls and the chat composer stay available.
- **Do** respect reduced-motion preferences, including portalled overlays.

### Don't:

- **Don't** transplant the public landing page's display typography or marketing spacing into the console.
- **Don't** replace semantic severity with the cyan or violet brand accent.
- **Don't** present missing data as zero, a completed intent as a passed check, or task inspection as authorization.
- **Don't** infer mobile acceptance, live agent validation or enforcement guarantees from the desktop synthetic fixtures.
