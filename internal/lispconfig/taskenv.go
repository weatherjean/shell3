package lispconfig

// TaskEnvironment maps declared runtime slots to the external task protocol.
// Callers separately control inherited credentials and leaf-worker markers.
func TaskEnvironment(slots map[string]string) []string {
	var env []string
	for _, binding := range [][2]string{
		{"task-id", "TASK_ID"},
		{"task-run", "TASK_RUN"},
		{"task-root", "TASK_ROOT"},
		{"task-artifacts", "TASK_ARTIFACTS"},
		{"task-attempt", "TASK_ATTEMPT"},
	} {
		if value, ok := slots[binding[0]]; ok {
			env = append(env, binding[1]+"="+value)
		}
	}
	return env
}
