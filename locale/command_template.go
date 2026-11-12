package locale

func init() {
	Register("Command parameters cannot be used as interpreter stdin programs; use fixed programs that read separate data", "Command parameters cannot be used as interpreter stdin programs; use fixed programs that read separate data")
	Register("Command parameters cannot be used to construct PowerShell script blocks; use fixed programs and separate data arguments", "Command parameters cannot be used to construct PowerShell script blocks; use fixed programs and separate data arguments")
	Register("Command parameters cannot be passed to expression-evaluating PowerShell commands; use fixed programs and separate data arguments", "Command parameters cannot be passed to expression-evaluating PowerShell commands; use fixed programs and separate data arguments")
	Register("Command parameters cannot be used as shell variable names or assignment options; use fixed names and separate data arguments", "Command parameters cannot be used as shell variable names or assignment options; use fixed names and separate data arguments")
	Register("Command parameters cannot be used where the shell evaluates arithmetic; compare them as text or pass separate data arguments", "Command parameters cannot be used where the shell evaluates arithmetic; compare them as text or pass separate data arguments")
	Register("Command parameters cannot be used in text the shell expands again, such as a prompt string or a startup file name; pass separate data arguments", "Command parameters cannot be used in text the shell expands again, such as a prompt string or a startup file name; pass separate data arguments")
	Register("Command parameters cannot be stored in shell variables after eval or trap text or a declaration that is not fixed; store them before it, write it literally, or pass separate data arguments", "Command parameters cannot be stored in shell variables after eval or trap text or a declaration that is not fixed; store them before it, write it literally, or pass separate data arguments")
	Register("Command tool parameters must be declared in its schema", "Command tool parameters must be declared in its schema")
	Register("HTTP tool parameters must be declared in its schema", "HTTP tool parameters must be declared in its schema")
	Register("Command tool parameter %s does not have its declared type", "Command tool parameter %s does not have its declared type")
	Register("HTTP tool parameter contains characters that are not allowed in the URL scheme or host", "HTTP tool parameter contains characters that are not allowed in the URL scheme or host")
	Register("HTTP tool parameter cannot be a dot segment of the URL path", "HTTP tool parameter cannot be a dot segment of the URL path")
	Register("Command parameters cannot be interpolated into nested shell or Python programs or their execution options; pass separate data arguments", "Command parameters cannot be interpolated into nested shell or Python programs or their execution options; pass separate data arguments")
	Register("Command parameters cannot be interpolated into CMD programs; use literal native arguments or a script data channel", "Command parameters cannot be interpolated into CMD programs; use literal native arguments or a script data channel")
}
