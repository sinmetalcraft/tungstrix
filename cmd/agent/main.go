package main

import (
	"context"
	"log"
	"os"

	"github.com/sinmetalcraft/tungstrix"
	"google.golang.org/adk/cmd/launcher/adk"
	"google.golang.org/adk/cmd/launcher/full"
	"google.golang.org/adk/server/restapi/services"
)

func main() {
	ctx := context.Background()

	root_agent, err := tungstrix.NewAgent(ctx)
	if err != nil {
		panic(err)
	}

	agentLoader, err := services.NewMultiAgentLoader(root_agent)
	if err != nil {
		log.Fatalf("failed to create agent loader: %v", err)
	}
	l := full.NewLauncher()
	if err := l.Execute(ctx, &adk.Config{
		AgentLoader: agentLoader,
	}, os.Args[1:]); err != nil {
		log.Fatalf("run failed: %v\n\n%s", err, l.CommandLineSyntax())
	}
}
