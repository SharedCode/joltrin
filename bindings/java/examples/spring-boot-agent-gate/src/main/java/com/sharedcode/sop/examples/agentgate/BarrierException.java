package com.sharedcode.sop.examples.agentgate;

/** The barrier could not be reached, or rejected the request itself (unknown workflow or step). */
public class BarrierException extends RuntimeException {
    public BarrierException(String message) {
        super(message);
    }

    public BarrierException(String message, Throwable cause) {
        super(message, cause);
    }
}
