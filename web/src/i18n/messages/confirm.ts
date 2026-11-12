// confirm messages. Confirmation dialogs for destructive actions. English is the source catalog; add the Spanish string for every key.

export const en = {
  cancel: "Cancel",
  confirm: "Confirm",
  delete: "Delete",
  remove: "Remove",
  restore: "Restore defaults",
  disconnect: "Disconnect",
  update: "Update",
  rollback: "Roll back",
  overwrite: "Overwrite",
  deleteProfileTitle: "Delete model profile “{p0}”?",
  deleteProfileDescription:
    "The profile and its API key are removed. Tasks and agents that use it fall back to the active profile or wait until you choose another.",
  deleteInterceptRuleTitle: "Delete approval rule “{p0}”?",
  deleteInterceptRuleDescription: "Commands this rule matched will be handled by the remaining rules.",
  deleteAssetRuleDescription: "Assets matching this pattern will no longer be blocked by this rule.",
  deleteToolTitle: "Delete custom tool “{p0}”?",
  deleteToolDescription: "The tool is removed and agents that use it can no longer call it.",
  resetToolTitle: "Restore “{p0}” to its defaults?",
  resetToolDescription:
    "Your changes to its description, parameters and agent assignments are replaced with the built-in defaults, and the tool is enabled again.",
  deleteGoalTitle: "Delete goal?",
  deleteGoalDescription: "“{p0}” will be removed from this task.",
  deleteConstraintTitle: "Delete constraint?",
  deleteConstraintDescription: "“{p0}” will no longer apply to this task.",
  removeScopeTitle: "Remove scope entry?",
  removeScopeDescription: "“{p0}” will no longer count as in scope for this task.",
  deleteTaskRuleTitle: "Delete task rule?",
  deleteTaskRuleDescription: "Assets matching “{p0}” will no longer be covered by this rule for this task.",
  removeGithubTokenTitle: "Remove the GitHub token?",
  removeGithubTokenDescription:
    "Counterweight will no longer be able to open pull requests or re-check merged fixes until you add a token again.",
  disconnectJiraTitle: "Disconnect Jira?",
  disconnectJiraDescription:
    "Existing Jira issues stay as they are, but Counterweight stops creating and updating them until you connect again.",
  restorePromptTitle: "Restore the default prompt?",
  restorePromptDescription:
    "Your edited prompt is replaced with the built-in default and saved as a new version. Earlier versions stay in the history.",
  restoreWrapupTitle: "Restore the default wrap-up?",
  restoreWrapupDescription: "Your custom wrap-up message and turn limit are cleared, and the built-in default applies.",
  deleteTriggerTitle: "Delete trigger?",
  deleteTriggerDescription: "This agent will no longer run when the trigger conditions are met.",
  overwriteSkillTitle: "Overwrite existing skill?",
};

export const es: Partial<Record<keyof typeof en, string>> = {
  cancel: "Cancelar",
  confirm: "Confirmar",
  delete: "Eliminar",
  remove: "Quitar",
  restore: "Restaurar valores",
  disconnect: "Desconectar",
  update: "Actualizar",
  rollback: "Revertir",
  overwrite: "Sobrescribir",
  deleteProfileTitle: "¿Eliminar el perfil de modelo “{p0}”?",
  deleteProfileDescription:
    "Se elimina el perfil y su clave de API. Las tareas y los agentes que lo usan pasan al perfil activo o esperan hasta que elijas otro.",
  deleteInterceptRuleTitle: "¿Eliminar la regla de aprobación “{p0}”?",
  deleteInterceptRuleDescription: "Los comandos que coincidían con esta regla serán tratados por las reglas restantes.",
  deleteAssetRuleDescription: "Los activos que coincidan con este patrón dejarán de ser bloqueados por esta regla.",
  deleteToolTitle: "¿Eliminar la herramienta personalizada “{p0}”?",
  deleteToolDescription: "Se elimina la herramienta y los agentes que la usan ya no podrán llamarla.",
  resetToolTitle: "¿Restaurar “{p0}” a sus valores por defecto?",
  resetToolDescription:
    "Tus cambios en la descripción, los parámetros y la asignación a agentes se reemplazan por los valores integrados, y la herramienta se vuelve a habilitar.",
  deleteGoalTitle: "¿Eliminar el objetivo?",
  deleteGoalDescription: "“{p0}” se quitará de esta tarea.",
  deleteConstraintTitle: "¿Eliminar la restricción?",
  deleteConstraintDescription: "“{p0}” dejará de aplicarse a esta tarea.",
  removeScopeTitle: "¿Quitar la entrada del alcance?",
  removeScopeDescription: "“{p0}” dejará de considerarse dentro del alcance de esta tarea.",
  deleteTaskRuleTitle: "¿Eliminar la regla de la tarea?",
  deleteTaskRuleDescription:
    "Los activos que coincidan con “{p0}” dejarán de estar cubiertos por esta regla en esta tarea.",
  removeGithubTokenTitle: "¿Quitar el token de GitHub?",
  removeGithubTokenDescription:
    "Counterweight no podrá abrir pull requests ni volver a comprobar las correcciones fusionadas hasta que agregues un token de nuevo.",
  disconnectJiraTitle: "¿Desconectar Jira?",
  disconnectJiraDescription:
    "Las incidencias de Jira existentes se conservan, pero Counterweight deja de crearlas y actualizarlas hasta que vuelvas a conectar.",
  restorePromptTitle: "¿Restaurar el prompt por defecto?",
  restorePromptDescription:
    "Tu prompt editado se reemplaza por el valor integrado y se guarda como una versión nueva. Las versiones anteriores quedan en el historial.",
  restoreWrapupTitle: "¿Restaurar el cierre por defecto?",
  restoreWrapupDescription:
    "Se borran tu mensaje de cierre personalizado y el límite de turnos, y se aplica el valor integrado.",
  deleteTriggerTitle: "¿Eliminar el disparador?",
  deleteTriggerDescription: "Este agente dejará de ejecutarse cuando se cumplan las condiciones del disparador.",
  overwriteSkillTitle: "¿Sobrescribir la habilidad existente?",
};
