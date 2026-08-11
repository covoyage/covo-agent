package agent

func (ca *CovoAgent) Trajectory() *TrajectoryRecorder {
	return ca.trajectory
}

func (ca *CovoAgent) CostTracker() *CostTracker {
	return ca.costTracker
}

func (ca *CovoAgent) ThinkScrubber() *StreamingThinkScrubber {
	return ca.thinkScrubber
}
