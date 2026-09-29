package com.sharedcode.sop;

import com.fasterxml.jackson.databind.JavaType;
import com.fasterxml.jackson.databind.ObjectMapper;
import org.junit.Test;

import java.nio.file.Path;
import java.util.List;

import static org.junit.Assert.assertThrows;

/**
 * BTree deserializes native-library JSON into caller-chosen key and value
 * types with a default ObjectMapper. These tests build the same parametric
 * List&lt;Item&lt;K, V&gt;&gt; type BTree uses and feed it hostile input, so a
 * jackson-databind downgrade that reopens the fixed advisories fails here.
 * They do not need the native library.
 */
public class JacksonHardeningTest {
    private final ObjectMapper mapper = new ObjectMapper();

    private <K, V> List<Item<K, V>> readItems(String json, Class<K> keyType, Class<V> valueType) throws Exception {
        JavaType itemType = mapper.getTypeFactory().constructParametricType(Item.class, keyType, valueType);
        JavaType listType = mapper.getTypeFactory().constructCollectionType(List.class, itemType);
        return mapper.readValue(json, listType);
    }

    private static String itemJson(String key, String value) {
        return "[{\"key\":" + key + ",\"value\":" + value + ",\"id\":\"x\"}]";
    }

    // GHSA-q4xh-88c3-wmh7: digits inside a JSON string bypass maxNumberLength.
    @Test(timeout = 20000)
    public void rejectsOversizedDurationString() {
        String digits = "9".repeat(300_000);
        String json = itemJson("\"P" + digits + "D\"", "\"v\"");
        assertThrows(Exception.class, () -> readItems(json, javax.xml.datatype.Duration.class, String.class));
    }

    @Test(timeout = 20000)
    public void rejectsOversizedXmlGregorianCalendarString() {
        String digits = "9".repeat(300_000);
        String json = itemJson("\"" + digits + "-01-01T00:00:00Z\"", "\"v\"");
        assertThrows(Exception.class, () -> readItems(json, javax.xml.datatype.XMLGregorianCalendar.class, String.class));
    }

    // GHSA-wjgm-6hv5-3cvf: Path values must not resolve arbitrary providers.
    @Test
    public void rejectsPathWithUnknownScheme() {
        String json = itemJson("\"nosuchscheme://host/path\"", "\"v\"");
        assertThrows(Exception.class, () -> readItems(json, Path.class, String.class));
    }
}
