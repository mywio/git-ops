package core

// TriggerReconcile is a channel to signal immediate reconciliation from plugins
var TriggerReconcile = make(chan struct{}, 1) // Buffered to avoid blocking if already reconciling
