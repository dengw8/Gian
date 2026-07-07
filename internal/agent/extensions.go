package agent

type PlanModeMiddleware struct {
	NoopMiddleware
}

func (m PlanModeMiddleware) Name() string {
	return "plan_mode"
}

type CompactionMiddleware struct {
	NoopMiddleware
}

func (m CompactionMiddleware) Name() string {
	return "compaction"
}

type SubAgentMiddleware struct {
	NoopMiddleware
}

func (m SubAgentMiddleware) Name() string {
	return "subagent"
}
