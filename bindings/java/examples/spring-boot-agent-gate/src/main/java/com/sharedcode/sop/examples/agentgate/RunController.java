package com.sharedcode.sop.examples.agentgate;

import jakarta.validation.Valid;
import jakarta.validation.constraints.NotBlank;
import org.springframework.http.HttpStatus;
import org.springframework.http.ProblemDetail;
import org.springframework.web.bind.annotation.ExceptionHandler;
import org.springframework.web.bind.annotation.PostMapping;
import org.springframework.web.bind.annotation.RequestBody;
import org.springframework.web.bind.annotation.RestController;

@RestController
public class RunController {
    public record RunRequest(@NotBlank String workflow, @NotBlank String traceId, @NotBlank String goal) {
    }

    private final PlanRunner runner;

    public RunController(PlanRunner runner) {
        this.runner = runner;
    }

    @PostMapping("/agent/runs")
    public RunResult run(@Valid @RequestBody RunRequest req) {
        return runner.run(req.workflow(), req.traceId(), req.goal());
    }

    @ExceptionHandler(BarrierException.class)
    public ProblemDetail barrierFailed(BarrierException e) {
        return ProblemDetail.forStatusAndDetail(HttpStatus.BAD_GATEWAY, e.getMessage());
    }
}
