// states messages. Shared loading, empty and error states. English is the source catalog; add the Spanish string for every key.

export const en = {
  tryAgain: "Try again",
  reloadPage: "Reload page",
  errorBoundaryTitle: "Something went wrong",
  errorBoundaryDetail: "This screen hit an unexpected error. Try again, or reload the page if the problem continues.",
  findingsLoadFailed: "Could not load findings",
  findingsRefreshFailed: "Could not refresh findings: {p0}",
  findingsEmptyAll: "No findings yet. Findings appear here when a task confirms a weakness.",
  findingsEmptyFiltered: "No findings match these filters.",
  goToTasks: "Go to Tasks",
  clearFilters: "Clear filters",
  findingLoadFailed: "Could not load this finding",
  profilesLoadFailed: "Could not load model profiles",
  settingsLoadFailed: "Could not load system settings",
  settingSaveFailed: "Could not save {p0}: {p1}",
  updateSigned: "Signatures verified",
  updateUnsigned: "Signatures not verified",
  updateSignedHint: "This build only installs updates signed by the maintainers.",
  updateUnsignedHint: "This build has no release keys: updates are checked against SHA256SUMS only.",
};

export const es: Partial<Record<keyof typeof en, string>> = {
  tryAgain: "Intentar de nuevo",
  reloadPage: "Recargar la página",
  errorBoundaryTitle: "Algo salió mal",
  errorBoundaryDetail:
    "Esta pantalla encontró un error inesperado. Intenta de nuevo, o recarga la página si el problema continúa.",
  findingsLoadFailed: "No se pudieron cargar los hallazgos",
  findingsRefreshFailed: "No se pudieron actualizar los hallazgos: {p0}",
  findingsEmptyAll: "Aún no hay hallazgos. Aparecen aquí cuando una tarea confirma una debilidad.",
  findingsEmptyFiltered: "Ningún hallazgo coincide con estos filtros.",
  goToTasks: "Ir a Tareas",
  clearFilters: "Quitar filtros",
  findingLoadFailed: "No se pudo cargar este hallazgo",
  profilesLoadFailed: "No se pudieron cargar los perfiles de modelo",
  settingsLoadFailed: "No se pudo cargar la configuración del sistema",
  settingSaveFailed: "No se pudo guardar {p0}: {p1}",
  updateSigned: "Firmas verificadas",
  updateUnsigned: "Firmas no verificadas",
  updateSignedHint: "Esta versión solo instala actualizaciones firmadas por los mantenedores.",
  updateUnsignedHint:
    "Esta versión no incluye claves de publicación: las actualizaciones se comprueban solo con SHA256SUMS.",
};
