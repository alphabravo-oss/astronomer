package auth

// NewToolUIGrantBackend creates the cluster-wide, atomic one-use store for
// browser UI launches. Values are encrypted by the gateway before storage.
func NewToolUIGrantBackend(redisURL string) (StreamTicketBackend, error) {
	if redisURL == "" {
		return newMemTicketBackend(), nil
	}
	client, err := redisClientFromAsynqURL(redisURL)
	if err != nil {
		return nil, err
	}
	return &redisTicketBackend{rdb: client, prefix: "tool-ui-launch:"}, nil
}
