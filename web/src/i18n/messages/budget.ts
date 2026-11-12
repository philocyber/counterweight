// budget messages. Per-task usage budget: create form, task header, settings and model prices. English is the source catalog; add the Spanish string for every key.

export const en = {
  // Create form
  tokenBudgetLabel: "Token budget",
  tokenBudgetHint: "Total input and output tokens this task may use. Empty uses the default; 0 means no limit.",
  defaultPlaceholder: "Default: {p0}",
  defaultPlaceholderNone: "Default: no limit",
  costBudgetLabel: "Cost budget",
  costBudgetHint:
    "Pauses the task when its estimated cost reaches this amount. Cost is estimated from the prices set on the model profiles. Empty or 0 means no limit.",
  currencyLabel: "Currency",
  invalidTokens: "The token budget must be a whole number, 0 or more.",
  invalidCost: "The cost budget must be a number, 0 or more.",
  invalidCurrency: "The currency must be a 3-letter code, for example USD.",
  costNeedsPrices: "Cost budgets need prices set on a model profile.",
  costNeedsPricesLink: "Set prices on the Models page",

  // Task header
  indicatorLabel: "Budget",
  tokensOfLimit: "{p0} of {p1} tokens",
  tokensUsed: "{p0} tokens used",
  costOfLimit: "Estimated cost {p0} of {p1} {p2}",
  costOnly: "Estimated cost {p0} {p1}",
  costUnknown: "Cost unknown for some calls",
  stateOk: "Within budget",
  stateWarning: "{p0}% used",
  stateReached: "Budget reached",
  editBudget: "Edit budget",
  setBudget: "Set budget",

  // Pause banner
  pausedTitle: "Paused: budget reached",
  pausedFallback: "The task used its whole budget and was paused. Nothing was lost.",
  pausedHint: "Raise the budget, then resume the task. Raising the budget does not resume it by itself.",
  raisedTitle: "Budget raised: the task can be resumed",
  raisedBody: "The task is still paused. Resume it to continue.",
  raiseAction: "Raise budget",
  resumeAction: "Resume",
  resumeFailed: "Could not resume: {p0}",

  // Dialog
  dialogTitle: "Task budget",
  dialogRaiseTitle: "Raise the task budget",
  dialogDescription:
    "Used so far: {p0} tokens. 0 removes a limit. A field left unchanged stays as it is. This does not resume the task.",
  dialogCostUsed: "Estimated cost so far: {p0} {p1}.",
  dialogNothingChanged: "Change at least one value.",
  dialogSave: "Save budget",
  dialogSaved: "Budget updated.",
  dialogSavedPaused: "Budget updated. The task is still paused: resume it to continue.",
  dialogCancel: "Cancel",

  // Settings
  defaultCardTitle: "Default token budget",
  defaultCardDescription:
    "Token budget given to new tasks that do not set their own. 0 means no limit. It does not change tasks that already exist.",
  defaultLabel: "Tokens per task",
  defaultInvalid: "Enter a whole number, 0 or more.",
  defaultSaved: "Default token budget saved.",

  // Model profile prices
  pricesTitle: "Prices for cost estimates",
  pricesNote:
    "Optional, per million tokens. Used only to estimate cost and to enforce a cost budget. Counterweight has no built-in price list, so enter the prices your provider charges.",
  priceInput: "Input",
  priceCachedInput: "Cached input",
  priceOutput: "Output",
  priceCurrency: "Currency",
  priceInvalid: "Prices must be numbers, 0 or more.",
};

export const es: Record<keyof typeof en, string> = {
  tokenBudgetLabel: "Presupuesto de tokens",
  tokenBudgetHint:
    "Total de tokens de entrada y salida que puede usar esta tarea. Vacío usa el valor predeterminado; 0 significa sin límite.",
  defaultPlaceholder: "Predeterminado: {p0}",
  defaultPlaceholderNone: "Predeterminado: sin límite",
  costBudgetLabel: "Presupuesto de costo",
  costBudgetHint:
    "Pausa la tarea cuando su costo estimado alcanza este importe. El costo se estima con los precios definidos en los perfiles de modelo. Vacío o 0 significa sin límite.",
  currencyLabel: "Moneda",
  invalidTokens: "El presupuesto de tokens debe ser un número entero, 0 o más.",
  invalidCost: "El presupuesto de costo debe ser un número, 0 o más.",
  invalidCurrency: "La moneda debe ser un código de 3 letras, por ejemplo USD.",
  costNeedsPrices: "Los presupuestos de costo necesitan precios definidos en un perfil de modelo.",
  costNeedsPricesLink: "Define los precios en la página de Modelos",

  indicatorLabel: "Presupuesto",
  tokensOfLimit: "{p0} de {p1} tokens",
  tokensUsed: "{p0} tokens usados",
  costOfLimit: "Costo estimado {p0} de {p1} {p2}",
  costOnly: "Costo estimado {p0} {p1}",
  costUnknown: "Costo desconocido en algunas llamadas",
  stateOk: "Dentro del presupuesto",
  stateWarning: "{p0}% usado",
  stateReached: "Presupuesto alcanzado",
  editBudget: "Editar presupuesto",
  setBudget: "Definir presupuesto",

  pausedTitle: "En pausa: presupuesto alcanzado",
  pausedFallback: "La tarea usó todo su presupuesto y se pausó. No se perdió nada.",
  pausedHint: "Sube el presupuesto y luego reanuda la tarea. Subir el presupuesto no la reanuda por sí solo.",
  raisedTitle: "Presupuesto aumentado: la tarea se puede reanudar",
  raisedBody: "La tarea sigue en pausa. Reanúdala para continuar.",
  raiseAction: "Subir presupuesto",
  resumeAction: "Reanudar",
  resumeFailed: "No se pudo reanudar: {p0}",

  dialogTitle: "Presupuesto de la tarea",
  dialogRaiseTitle: "Subir el presupuesto de la tarea",
  dialogDescription:
    "Usado hasta ahora: {p0} tokens. 0 quita un límite. Un campo sin cambios se queda como está. Esto no reanuda la tarea.",
  dialogCostUsed: "Costo estimado hasta ahora: {p0} {p1}.",
  dialogNothingChanged: "Cambia al menos un valor.",
  dialogSave: "Guardar presupuesto",
  dialogSaved: "Presupuesto actualizado.",
  dialogSavedPaused: "Presupuesto actualizado. La tarea sigue en pausa: reanúdala para continuar.",
  dialogCancel: "Cancelar",

  defaultCardTitle: "Presupuesto de tokens predeterminado",
  defaultCardDescription:
    "Presupuesto de tokens que reciben las tareas nuevas que no definen el suyo. 0 significa sin límite. No cambia las tareas que ya existen.",
  defaultLabel: "Tokens por tarea",
  defaultInvalid: "Introduce un número entero, 0 o más.",
  defaultSaved: "Presupuesto de tokens predeterminado guardado.",

  pricesTitle: "Precios para estimar el costo",
  pricesNote:
    "Opcional, por millón de tokens. Se usan solo para estimar el costo y aplicar un presupuesto de costo. Counterweight no incluye una lista de precios, así que introduce los que cobra tu proveedor.",
  priceInput: "Entrada",
  priceCachedInput: "Entrada en caché",
  priceOutput: "Salida",
  priceCurrency: "Moneda",
  priceInvalid: "Los precios deben ser números, 0 o más.",
};
