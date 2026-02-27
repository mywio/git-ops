package plugins

type EventType string

type Event struct {
	Type    EventType
	Repo    string
	Owner   string
	Message string
	Extra   map[string]interface{} // for future extensibility
}

type Notifier interface {
	Name() string
	Notify(event Event) error
	Init(config map[string]string) error
}
