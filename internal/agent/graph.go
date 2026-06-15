package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/compose"
)

// BuildGraph constructs the Reading Memory Agent graph and compiles it into a runnable.
// The returned Runnable is safe for concurrent reuse — call Invoke for each question.
func (d *GraphDeps) BuildGraph(ctx context.Context) (compose.Runnable[*ReadingAgentState, *ReadingAgentState], error) {
	g := compose.NewGraph[*ReadingAgentState, *ReadingAgentState]()

	// --- Register nodes ---

	if err := g.AddLambdaNode("router", compose.InvokableLambda(d.routerNode)); err != nil {
		return nil, fmt.Errorf("add router node: %w", err)
	}
	if err := g.AddLambdaNode("search", compose.InvokableLambda(d.searchNode)); err != nil {
		return nil, fmt.Errorf("add search node: %w", err)
	}
	if err := g.AddLambdaNode("graph", compose.InvokableLambda(d.graphNode)); err != nil {
		return nil, fmt.Errorf("add graph node: %w", err)
	}
	if err := g.AddLambdaNode("verify", compose.InvokableLambda(d.verifyNode)); err != nil {
		return nil, fmt.Errorf("add verify node: %w", err)
	}
	if err := g.AddLambdaNode("rewrite", compose.InvokableLambda(d.rewriteNode)); err != nil {
		return nil, fmt.Errorf("add rewrite node: %w", err)
	}
	if err := g.AddLambdaNode("generate", compose.InvokableLambda(d.generateNode)); err != nil {
		return nil, fmt.Errorf("add generate node: %w", err)
	}

	// --- Wire edges ---

	// START → Router
	if err := g.AddEdge(compose.START, "router"); err != nil {
		return nil, fmt.Errorf("add start→router edge: %w", err)
	}

	// Router branch: search (fact/mixed) or graph (timeline/relation)
	if err := g.AddBranch("router", compose.NewGraphBranch(d.classifyBranch, map[string]bool{
		"search": true,
		"graph":  true,
	})); err != nil {
		return nil, fmt.Errorf("add router branch: %w", err)
	}

	// Search → Verify
	if err := g.AddEdge("search", "verify"); err != nil {
		return nil, fmt.Errorf("add search→verify edge: %w", err)
	}

	// Graph → Generate (no verification needed for structured graph data)
	if err := g.AddEdge("graph", "generate"); err != nil {
		return nil, fmt.Errorf("add graph→generate edge: %w", err)
	}

	// Verify branch: rewrite (loop) or generate (done)
	if err := g.AddBranch("verify", compose.NewGraphBranch(d.verifyBranch, map[string]bool{
		"rewrite":  true,
		"generate": true,
	})); err != nil {
		return nil, fmt.Errorf("add verify branch: %w", err)
	}

	// Rewrite → Search (loop back)
	if err := g.AddEdge("rewrite", "search"); err != nil {
		return nil, fmt.Errorf("add rewrite→search edge: %w", err)
	}

	// Generate → END
	if err := g.AddEdge("generate", compose.END); err != nil {
		return nil, fmt.Errorf("add generate→end edge: %w", err)
	}

	// --- Compile ---
	runnable, err := g.Compile(ctx, compose.WithGraphName("reading-memory-agent"))
	if err != nil {
		return nil, fmt.Errorf("compile graph: %w", err)
	}

	return runnable, nil
}
