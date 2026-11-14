package com.sharedcode.sop.examples.agentgate;

import java.util.List;

public record RunResult(Status status, List<String> trace, List<Decision> decisions, String reason) {

    public enum Status { COMPLETED, NEEDS_HUMAN, BUDGET_EXHAUSTED }

    /** One barrier decision, in the order the agent hit it. */
    public record Decision(String step, boolean allowed, String missingState) {
    }
}
