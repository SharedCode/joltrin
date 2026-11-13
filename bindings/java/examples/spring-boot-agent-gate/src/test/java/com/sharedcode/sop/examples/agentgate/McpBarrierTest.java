package com.sharedcode.sop.examples.agentgate;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.fasterxml.jackson.databind.ObjectMapper;
import com.sun.net.httpserver.HttpServer;
import java.net.InetSocketAddress;
import java.nio.charset.StandardCharsets;
import java.util.concurrent.atomic.AtomicReference;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;
import org.springframework.web.client.RestClient;

class McpBarrierTest {
    HttpServer server;
    final AtomicReference<String> reply = new AtomicReference<>();
    final AtomicReference<String> auth = new AtomicReference<>();
    final AtomicReference<String> body = new AtomicReference<>();

    @BeforeEach
    void start() throws Exception {
        server = HttpServer.create(new InetSocketAddress("127.0.0.1", 0), 0);
        server.createContext("/mcp", ex -> {
            auth.set(ex.getRequestHeaders().getFirst("Authorization"));
            body.set(new String(ex.getRequestBody().readAllBytes(), StandardCharsets.UTF_8));
            byte[] out = reply.get().getBytes(StandardCharsets.UTF_8);
            ex.getResponseHeaders().add("Content-Type", "application/json");
            ex.sendResponseHeaders(200, out.length);
            ex.getResponseBody().write(out);
            ex.close();
        });
        server.start();
    }

    @AfterEach
    void stop() {
        server.stop(0);
    }

    McpBarrier barrier(String token) {
        String url = "http://127.0.0.1:" + server.getAddress().getPort() + "/mcp";
        return new McpBarrier(new BarrierProperties(url, token, 20), new ObjectMapper(), RestClient.builder());
    }

    @Test
    void parsesABlockedResultAndSendsTheBearerToken() {
        reply.set("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"structuredContent\":{\"executed\":false,"
                + "\"blocked\":{\"blocked_by\":\"precondition\",\"missing_state\":\"backup_validated\","
                + "\"established_by_steps\":[\"validate_backup\"],\"attempts\":1,\"next\":\"run_established_by_steps\"}}}}");
        StepResult r = barrier("secret-token-123456").execute("db-maintenance", "t1", "drop_prod_db");
        assertFalse(r.executed());
        assertEquals("backup_validated", r.blocked().missingState());
        assertEquals("validate_backup", r.blocked().establishedBySteps().get(0));
        assertEquals("Bearer secret-token-123456", auth.get());
        assertTrue(body.get().contains("\"trace_id\":\"t1\""));
    }

    @Test
    void turnsAnMcpToolErrorIntoABarrierException() {
        reply.set("{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"isError\":true,"
                + "\"structuredContent\":{\"error\":\"unknown workflow \\\"nope\\\"\"}}}");
        BarrierException e = assertThrows(BarrierException.class, () -> barrier("").execute("nope", "t", "x"));
        assertTrue(e.getMessage().contains("unknown workflow"));
    }
}
