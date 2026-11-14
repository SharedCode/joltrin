package com.sharedcode.sop.examples.agentgate;

/** The one tool call the agent loop needs from the verification barrier. */
public interface Barrier {
    StepResult execute(String workflow, String traceId, String step);
}
