// prreview messages. Code review scoped to a change (pull request or base and head): new-task form, task card and finding badge. English is the source catalog; add the Spanish string for every key.

export const en = {
  // New task form
  scopeLegend: "Review a change",
  scopeOptional: "(optional)",
  scopeRepository: "Whole repository",
  scopeRepositoryHint: "Review the code as it is on the branch above. This is the default.",
  scopePull: "A pull request",
  scopePullHint:
    "Start from the changes in one pull request. The reviewers still read the rest of the code for context.",
  scopeRange: "Between two points",
  scopeRangeHint: "Start from what changed between a base and a head. Each can be a branch, a tag or a commit.",
  pullLabel: "Pull request number",
  pullPlaceholder: "123",
  pullHint: "The number in the pull request address, for example 123 in github.com/owner/name/pull/123.",
  baseLabel: "Base",
  basePlaceholder: "main",
  baseHint: "The branch, tag or commit to compare against.",
  headLabel: "Head",
  headPlaceholder: "feature/login",
  headHint: "The branch, tag or commit that contains the changes.",
  noRefNote: "A change review uses the change you name, so the branch or commit field is not used.",
  pullRequired: "Enter the pull request number.",
  pullInvalid: "The pull request number must be a whole number of 1 or more.",
  baseRequired: "Enter the base.",
  baseInvalid:
    "The base can use letters, digits, dots, underscores, hyphens and slashes. It cannot start with a hyphen or contain two dots in a row.",
  headRequired: "Enter the head.",
  headInvalid:
    "The head can use letters, digits, dots, underscores, hyphens and slashes. It cannot start with a hyphen or contain two dots in a row.",
  repositoryHasRef:
    "Remove the part after # from the repository. A change review names its change in the fields below.",

  // Task detail card
  changeLabel: "Change",
  changePull: "Pull request #{p0}",
  changeRange: "{p0} to {p1}",
  changeBase: "Base",
  changeHead: "Head",
  changeFiles: "Changed files",
  changeFilesCount: "{p0} files",
  changeFilesOne: "1 file",
  changeOpenPull: "Open the pull request on GitHub",
  changeFilesTruncated:
    "The list of changed files was cut. Findings in files that are not listed count as inside the change.",
  changeDiffTruncated: "The change text shown to the reviewers was cut. They can still read every file.",

  // Findings
  outsideChange: "Outside the change",
  outsideChangeTip: "The problem is in code this change did not modify.",
};

export const es: Record<keyof typeof en, string> = {
  scopeLegend: "Revisar un cambio",
  scopeOptional: "(opcional)",
  scopeRepository: "Repositorio completo",
  scopeRepositoryHint: "Revisa el código como está en la rama indicada arriba. Es la opción por defecto.",
  scopePull: "Un pull request",
  scopePullHint:
    "Parte de los cambios de un pull request. Los revisores siguen leyendo el resto del código como contexto.",
  scopeRange: "Entre dos puntos",
  scopeRangeHint:
    "Parte de lo que cambió entre una base y un head. Cada uno puede ser una rama, una etiqueta o un commit.",
  pullLabel: "Número del pull request",
  pullPlaceholder: "123",
  pullHint:
    "El número que aparece en la dirección del pull request, por ejemplo 123 en github.com/owner/name/pull/123.",
  baseLabel: "Base",
  basePlaceholder: "main",
  baseHint: "La rama, etiqueta o commit con la que se compara.",
  headLabel: "Head",
  headPlaceholder: "feature/login",
  headHint: "La rama, etiqueta o commit que contiene los cambios.",
  noRefNote: "Una revisión de cambio usa el cambio que indiques, así que el campo de rama o commit no se usa.",
  pullRequired: "Ingresa el número del pull request.",
  pullInvalid: "El número del pull request debe ser un entero de 1 o más.",
  baseRequired: "Ingresa la base.",
  baseInvalid:
    "La base puede usar letras, dígitos, puntos, guiones bajos, guiones y barras. No puede empezar con guion ni tener dos puntos seguidos.",
  headRequired: "Ingresa el head.",
  headInvalid:
    "El head puede usar letras, dígitos, puntos, guiones bajos, guiones y barras. No puede empezar con guion ni tener dos puntos seguidos.",
  repositoryHasRef:
    "Quita la parte posterior a # del repositorio. Una revisión de cambio indica su cambio en los campos de abajo.",

  changeLabel: "Cambio",
  changePull: "Pull request #{p0}",
  changeRange: "{p0} a {p1}",
  changeBase: "Base",
  changeHead: "Head",
  changeFiles: "Archivos modificados",
  changeFilesCount: "{p0} archivos",
  changeFilesOne: "1 archivo",
  changeOpenPull: "Abrir el pull request en GitHub",
  changeFilesTruncated:
    "La lista de archivos modificados se recortó. Los hallazgos en archivos que no aparecen cuentan como dentro del cambio.",
  changeDiffTruncated:
    "El texto del cambio que ven los revisores se recortó. Ellos pueden seguir leyendo todos los archivos.",

  outsideChange: "Fuera del cambio",
  outsideChangeTip: "El problema está en código que este cambio no modificó.",
};
