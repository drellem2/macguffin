package workitem

// actorFor returns a best-effort actor identifier for an event. Prefers the
// item's assignee, then the creator, falling back to the current OS user.
func actorFor(item *Item) string {
	if item != nil {
		if item.Assignee != "" {
			return item.Assignee
		}
		if item.Creator != "" {
			return item.Creator
		}
	}
	return currentUser()
}
