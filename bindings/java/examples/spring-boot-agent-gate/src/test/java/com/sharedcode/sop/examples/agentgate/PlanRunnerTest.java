package com.sharedcode.sop.examples.agentgate;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import java.util.ArrayList;
import java.util.List;
import java.util.Map;
import org.junit.jupiter.api.Test;

class PlanRunnerTest {
    /** In-memory barrier with the db-maintenance rules: each step needs the previous step's state. */
    static class FakeBarrier implements Barrier {
        final Map<String, String> needs = Map.of("validate_backup", "take_backup", "drop_prod_db", "validate_backup");
        final List<String> trace = new ArrayList<>();
        int calls;

        public StepResult execute(String workflow, String traceId, String step) {
            calls++;
            String need = needs.get(step);
            if (need != null && !trace.contains(need)) {
                return new StepResult(false, null, trace, new StepResult.Blocked("precondition", need + "_done",
                        "step " + step + " requires " + need, "", List.of(need), 1, "run_established_by_steps"));
            }
            trace.add(step);
            return new StepResult(true, step, List.copyOf(trace), null);
        }
    }

    @Test
    void runsPrerequisitesInOrderThenTheGoal() {
        PlanRunner runner = new PlanRunner(new FakeBarrier(), new BarrierProperties("", "", 20));
        RunResult r = runner.run("db-maintenance", "t1", "drop_prod_db");
        assertEquals(RunResult.Status.COMPLETED, r.status());
        assertEquals(List.of("take_backup", "validate_backup", "drop_prod_db"), r.trace());
    }

    @Test
    void stopsForAPersonWhenNothingEstablishesTheMissingState() {
        Barrier stuck = (w, t, s) -> new StepResult(false, null, List.of(),
                new StepResult.Blocked("rule", "approved", "needs approval", "", List.of(), 1, "stop_and_ask"));
        RunResult r = new PlanRunner(stuck, new BarrierProperties("", "", 20)).run("w", "t", "deploy");
        assertEquals(RunResult.Status.NEEDS_HUMAN, r.status());
        assertEquals("needs approval", r.reason());
    }

    @Test
    void triesTheNextEstablishingStepWhenTheFirstOneCannotRun() {
        List<String> done = new ArrayList<>();
        Barrier b = (w, t, s) -> {
            if (s.equals("manual_approval")) {
                return new StepResult(false, null, List.copyOf(done), new StepResult.Blocked("rule", "person",
                        "needs a person", "", List.of(), 1, "stop_and_ask"));
            }
            if (s.equals("deploy") && !done.contains("auto_approval")) {
                return new StepResult(false, null, List.copyOf(done), new StepResult.Blocked("precondition", "ready",
                        "blocked", "", List.of("manual_approval", "auto_approval"), 1, "run_established_by_steps"));
            }
            done.add(s);
            return new StepResult(true, s, List.copyOf(done), null);
        };
        RunResult r = new PlanRunner(b, new BarrierProperties("", "", 20)).run("w", "t", "deploy");
        assertEquals(RunResult.Status.COMPLETED, r.status());
        assertEquals(List.of("auto_approval", "deploy"), r.trace());
    }

    @Test
    void stopsAtTheCallBudgetInsteadOfLooping() {
        Barrier loops = (w, t, s) -> new StepResult(false, null, List.of(),
                new StepResult.Blocked("precondition", "x", "blocked", "", List.of("a"), 1, "run_established_by_steps"));
        RunResult r = new PlanRunner(loops, new BarrierProperties("", "", 5)).run("w", "t", "goal");
        assertEquals(RunResult.Status.BUDGET_EXHAUSTED, r.status());
        assertTrue(r.decisions().size() <= 5);
    }
}
