package com.sharedcode.sop.examples.agentgate;

import org.springframework.boot.context.properties.ConfigurationProperties;

@ConfigurationProperties("joltrin.barrier")
public record BarrierProperties(String url, String token, int maxCalls) {
}
