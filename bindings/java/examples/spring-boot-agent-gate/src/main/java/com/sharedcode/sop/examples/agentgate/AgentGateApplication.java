package com.sharedcode.sop.examples.agentgate;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.boot.context.properties.ConfigurationPropertiesScan;

@SpringBootApplication
@ConfigurationPropertiesScan
public class AgentGateApplication {
    public static void main(String[] args) {
        SpringApplication.run(AgentGateApplication.class, args);
    }
}
