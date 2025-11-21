package tungstrix

import (
	"context"
	"fmt"

	"github.com/sinmetalcraft/tungstrix/tools/spanner"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model/gemini"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

func NewAgent(ctx context.Context) (agent.Agent, error) {
	model, err := gemini.NewModel(ctx, "gemini-2.5-pro", &genai.ClientConfig{
		HTTPOptions: genai.HTTPOptions{APIVersion: "v1"},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create new model: %w", err)
	}

	analyzeQueryTool, err := functiontool.New(functiontool.Config{
		Name:        "analyzeQuery",
		Description: "Retrieve the Spanner QueryPlan.",
	}, spanner.AnalyzeQuery)

	a, err := llmagent.New(llmagent.Config{
		Name:        "tungstrix",
		Model:       model,
		Description: "Agent that can provide advice about Spanner",
		Instruction: prompt,
		Tools:       []tool.Tool{analyzeQueryTool},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create new agent: %w", err)
	}

	return a, nil
}
