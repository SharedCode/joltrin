package agent

import (
	"context"
	"fmt"
	"strings"

	log "log/slog"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/internal/logsafe"
)

type routingAnchor struct {
	prefix   string
	entity   string
	domain   string
	artifact string
	taskCtx  *TaskContextClassification
}

func isRoutingTestGenerator(gen ai.Generator) bool {
	if gen == nil {
		return false
	}
	typeName := fmt.Sprintf("%T", gen)
	return strings.Contains(typeName, "mock") || strings.Contains(typeName, "Mock") || strings.Contains(typeName, "Smart")
}

func parseRoutingAnchor(query string) *routingAnchor {

	log.Info("query: ", "query", logsafe.V(query))
	parts := strings.Split(query, ":")
	if len(parts) <= 1 {
		return nil
	}
	if !(strings.EqualFold(parts[0], "omni")) {
		return nil
	}
	anchor := &routingAnchor{
		prefix: parts[0],
		entity: parts[0],
	}
	if len(parts) >= 2 {
		anchor.domain = strings.TrimSpace(parts[1])
	}
	if len(parts) >= 3 {
		anchor.artifact = strings.TrimSpace(parts[2])
	}

	log.Info("anchor ", "anchor", anchor)

	anchor.taskCtx = enrichFocusedTaskContext(nil, anchor.entity, anchor.domain, anchor.artifact)
	return anchor
}

func (a *CopilotAgent) resetRoutingForTopicSwitch(query string, payload *ai.SessionPayload) {
	if payload != nil && payload.Variables != nil {
		delete(payload.Variables, "RoutingState")
	}
	a.clearMRUForTopicSwitch()
	if a.service != nil && a.service.session != nil && a.service.session.Memory != nil {
		newThreadID := sop.NewUUID()
		thread := &ConversationThread{
			ID:         newThreadID,
			RootPrompt: query,
			Label:      "Topic Switch",
			Category:   "General",
			Exchanges:  make([]Interaction, 0),
			Status:     "active",
		}
		a.service.session.Memory.AddThread(thread)
		a.service.session.Memory.CurrentThreadID = newThreadID
	}
}

func (a *CopilotAgent) persistRoutingState(ctx context.Context, taskCtx *TaskContextClassification) {
	if taskCtx == nil {
		return
	}
	if p := ai.GetSessionPayload(ctx); p != nil {
		if p.Variables == nil {
			p.Variables = make(map[string]any)
		}
		p.Variables["RoutingState"] = taskCtx
	}
	if a.service != nil && a.service.session != nil && a.service.session.Memory != nil {
		a.service.session.Memory.SetRoutingState(taskCtx)
	}
}

func (a *CopilotAgent) tryPathStyleRouting(ctx context.Context, query string) *TaskContextClassification {

	log.Info("Path-Style Routing Activated", "path_query", query)
	taskCtx := buildFocusedFallbackTaskContext("Omni", SpacesDomain, query)
	taskCtx.RoutingGate = RoutingGateFocused
	annotateTaskContextIntent(taskCtx, query)
	a.persistRoutingState(ctx, taskCtx)
	return taskCtx
}

func (a *CopilotAgent) tryPrefixBasedRouting(ctx context.Context, query string, gen ai.Generator, isTest bool, anchor *routingAnchor) (*TaskContextClassification, error) {
	if anchor == nil {
		return nil, nil
	}

	log.Info("Prefix-Based Routing Activated", "prefix", logsafe.V(anchor.prefix))

	var taskCtx *TaskContextClassification
	if !isTest && gen != nil {
		var err error
		taskCtx, err = a.ClassifyFocusedTaskContext(ctx, query, anchor.entity, anchor.domain, anchor.artifact, gen)
		if err != nil {
			return nil, err
		}
	}

	taskCtx = enrichFocusedTaskContext(taskCtx, anchor.entity, anchor.domain, anchor.artifact)
	if p := ai.GetSessionPayload(ctx); p != nil && p.Variables != nil {
		if rs, ok := p.Variables["RoutingState"].(*TaskContextClassification); ok && rs != nil && !isTest && gen != nil {
			updatedRS, isSwitch, err := a.ClassifyContinuityTaskContext(ctx, query, rs, taskCtx, gen)
			if err == nil && isSwitch {
				log.Info("Prefix-Based Routing Triggered Topic Switch Reset")
				a.resetRoutingForTopicSwitch(query, p)
			} else if err == nil && updatedRS != nil {
				taskCtx = enforceFocusedConstraints(updatedRS, anchor.entity, anchor.domain, anchor.artifact)
			}
		}
	}

	annotateTaskContextIntent(taskCtx, query)
	taskCtx.RoutingGate = RoutingGateFocused
	a.persistRoutingState(ctx, taskCtx)
	return taskCtx, nil
}

func (a *CopilotAgent) tryAskContinuationBasedRouting(ctx context.Context, query string, gen ai.Generator, isTest bool, anchor *routingAnchor) (*TaskContextClassification, error) {
	p := ai.GetSessionPayload(ctx)
	if p == nil || p.Variables == nil {
		return nil, nil
	}
	rs, ok := p.Variables["RoutingState"].(*TaskContextClassification)
	if !ok || rs == nil {
		return nil, nil
	}

	if !isTest && gen != nil {
		var anchorTaskCtx *TaskContextClassification
		if anchor != nil {
			anchorTaskCtx = anchor.taskCtx
		}
		updatedRS, isSwitch, err := a.ClassifyContinuityTaskContext(ctx, query, rs, anchorTaskCtx, gen)
		if err == nil && isSwitch {
			log.Info("Ask-Continuation Routing Detected Topic Switch. Falling through to Cold-Start Routing.")
			a.resetRoutingForTopicSwitch(query, p)
			return nil, nil
		}
		if err == nil && updatedRS != nil {
			log.Info("Ask-Continuation Routing Activated: Inheriting MRU Context with Updates", "domain", logsafe.V(updatedRS.Domain))
			annotateTaskContextIntent(updatedRS, query)
			updatedRS.RoutingGate = RoutingGateContinuity
			a.persistRoutingState(ctx, updatedRS)
			return updatedRS, nil
		}
		return nil, err
	}

	log.Info("Ask-Continuation Routing Activated: Inheriting MRU Context (Test Mode)", "domain", logsafe.V(rs.Domain))
	annotateTaskContextIntent(rs, query)
	rs.RoutingGate = RoutingGateContinuity
	a.persistRoutingState(ctx, rs)
	return rs, nil
}

func (a *CopilotAgent) tryColdStartBasedRouting(ctx context.Context, query string, gen ai.Generator, isTest bool) (*TaskContextClassification, error) {
	if len(strings.Split(query, ":")) == 1 {
		log.Info("Cold-Start Routing Activated")
	}
	if gen == nil || isTest {
		return nil, nil
	}

	taskCtx, err := a.ClassifyTaskContext(ctx, query, gen)
	if err != nil || taskCtx == nil {
		log.Warn("Cold-start routing classification failed or returned nil", "error", err)
		return nil, err
	}

	log.Info("Cold-start routing classification success", "domain", taskCtx.Domain)
	annotateTaskContextIntent(taskCtx, query)
	taskCtx.RoutingGate = RoutingGateDiscovery
	a.persistRoutingState(ctx, taskCtx)
	return taskCtx, nil
}
