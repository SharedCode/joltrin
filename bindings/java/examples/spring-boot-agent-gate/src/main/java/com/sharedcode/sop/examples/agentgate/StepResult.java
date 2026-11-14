package com.sharedcode.sop.examples.agentgate;

import com.fasterxml.jackson.annotation.JsonIgnoreProperties;
import com.fasterxml.jackson.databind.PropertyNamingStrategies;
import com.fasterxml.jackson.databind.annotation.JsonNaming;
import java.util.List;

/** What the barrier's execute_step tool returns for one call. */
@JsonIgnoreProperties(ignoreUnknown = true)
@JsonNaming(PropertyNamingStrategies.SnakeCaseStrategy.class)
public record StepResult(boolean executed, String step, List<String> trace, Blocked blocked) {

    @JsonIgnoreProperties(ignoreUnknown = true)
    @JsonNaming(PropertyNamingStrategies.SnakeCaseStrategy.class)
    public record Blocked(String blockedBy, String missingState, String message, String why,
                          List<String> establishedBySteps, int attempts, String next) {
    }
}
