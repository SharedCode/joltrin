package com.sharedcode.sop.examples.agentgate;

import com.fasterxml.jackson.databind.JsonNode;
import com.fasterxml.jackson.databind.ObjectMapper;
import java.util.Map;
import org.springframework.stereotype.Component;
import org.springframework.web.client.RestClient;
import org.springframework.web.client.RestClientException;

/** Calls sop-mcp-server over MCP streamable HTTP: one JSON-RPC tools/call per request. */
@Component
public class McpBarrier implements Barrier {
    private final RestClient http;
    private final ObjectMapper mapper;

    public McpBarrier(BarrierProperties props, ObjectMapper mapper, RestClient.Builder builder) {
        this.mapper = mapper;
        RestClient.Builder b = builder.baseUrl(props.url())
                .defaultHeader("Content-Type", "application/json")
                .defaultHeader("Accept", "application/json, text/event-stream");
        if (props.token() != null && !props.token().isBlank()) {
            b.defaultHeader("Authorization", "Bearer " + props.token());
        }
        this.http = b.build();
    }

    @Override
    public StepResult execute(String workflow, String traceId, String step) {
        Map<String, Object> call = Map.of("jsonrpc", "2.0", "id", 1, "method", "tools/call",
                "params", Map.of("name", "execute_step",
                        "arguments", Map.of("workflow", workflow, "trace_id", traceId, "step", step)));
        JsonNode reply;
        try {
            reply = http.post().body(call).retrieve().body(JsonNode.class);
        } catch (RestClientException e) {
            throw new BarrierException("barrier unreachable: " + e.getMessage(), e);
        }
        if (reply == null || reply.has("error")) {
            throw new BarrierException("barrier error: " + (reply == null ? "empty reply" : reply.get("error")));
        }
        JsonNode result = reply.path("result");
        JsonNode body = result.path("structuredContent");
        if (result.path("isError").asBoolean(false)) {
            throw new BarrierException(body.path("error").asText("barrier rejected the call"));
        }
        return mapper.convertValue(body, StepResult.class);
    }
}
