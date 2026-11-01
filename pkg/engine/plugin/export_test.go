package plugin

// BuiltExample returns the directory of an example plugin built by TestMain.
func BuiltExample(name string) string { return built[name] }
