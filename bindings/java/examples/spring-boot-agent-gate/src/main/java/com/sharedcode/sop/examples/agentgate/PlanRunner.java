package com.sharedcode.sop.examples.agentgate;

import java.util.ArrayList;
import java.util.List;
import org.springframework.stereotype.Service;

/**
 * A small agent loop. It asks the barrier to run the goal step. If the barrier blocks it and names
 * the steps that establish the missing state, the loop runs those first (recursively) and retries.
 * If nothing establishes the state, it stops and hands the decision to a person. It never works
 * around a block, and it stops after maxCalls so a bad runbook cannot spin forever.
 */
@Service
public class PlanRunner {
    private final Barrier barrier;
    private final int maxCalls;

    public PlanRunner(Barrier barrier, BarrierProperties props) {
        this.barrier = barrier;
        this.maxCalls = props.maxCalls();
    }

    public RunResult run(String workflow, String traceId, String goal) {
        Run run = new Run(workflow, traceId);
        run.pursue(goal);
        RunResult.Status status = run.stopReason == null ? RunResult.Status.COMPLETED
                : run.budgetHit ? RunResult.Status.BUDGET_EXHAUSTED : RunResult.Status.NEEDS_HUMAN;
        return new RunResult(status, run.trace, run.decisions, run.stopReason);
    }

    private final class Run {
        final String workflow;
        final String traceId;
        final List<RunResult.Decision> decisions = new ArrayList<>();
        List<String> trace = List.of();
        String stopReason;
        boolean budgetHit;
        int calls;

        Run(String workflow, String traceId) {
            this.workflow = workflow;
            this.traceId = traceId;
        }

        boolean pursue(String step) {
            while (true) {
                if (calls++ >= maxCalls) {
                    budgetHit = true;
                    stopReason = "stopped after " + maxCalls + " barrier calls";
                    return false;
                }
                StepResult r = barrier.execute(workflow, traceId, step);
                if (r.executed()) {
                    decisions.add(new RunResult.Decision(step, true, null));
                    trace = r.trace();
                    return true;
                }
                StepResult.Blocked b = r.blocked();
                decisions.add(new RunResult.Decision(step, false, b.missingState()));
                List<String> fixes = b.establishedBySteps();
                if (!"run_established_by_steps".equals(b.next()) || fixes == null || fixes.isEmpty()) {
                    stopReason = b.message();
                    return false;
                }
                if (!pursue(fixes.get(0))) {
                    return false;
                }
            }
        }
    }
}
