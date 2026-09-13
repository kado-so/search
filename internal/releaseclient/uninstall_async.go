package releaseclient

// PendingUninstall reports asynchronous Windows removal honestly. The result
// file contains the final outcome after the invoking executables have exited.
type PendingUninstall struct{ ResultPath string }

func (e *PendingUninstall) Error() string { return "uninstall pending; result: " + e.ResultPath }
