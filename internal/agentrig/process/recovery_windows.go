package process

func verifyOwnership(e commandEvidence) error {
	switch e.State {
	case "prepared", "stopped":
		return nil
	default:
		// Closing the worker's job handle starts asynchronous termination. A
		// missing job name, missing PID, or elapsed time cannot prove completion.
		return ErrWritersActive
	}
}
