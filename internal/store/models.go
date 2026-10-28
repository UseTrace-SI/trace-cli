package store

type Session struct {
	ID, Agent, AgentSID, Worktree string
	StartedAt                     int64
}

type Turn struct {
	ID, SessionID, Prompt, Reasoning, ToolCalls, FilesRead string
	Seq, StartedAt, EndedAt                                int64
}

type Keyframe struct {
	ID, TurnID, ManifestHash, GitHead, Tag string
	Seq, CreatedAt                         int64
}

type Op struct {
	ID, TurnID, SessionID, Path, Kind, Detail, BeforeHash, AfterHash, Source string
	StartLine, EndLine, At                                                   int64
}

type Commit struct {
	SHA, KeyframeID string
	At              int64
}
