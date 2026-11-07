# Proposal: Semantic Obfuscation for Privacy-Preserving LLM Interaction

## 1. Problem Statement
To support HIPAA and corporate data privacy policies, we should avoid sending Protected Health Information (PHI) or sensitive PII (Personally Identifiable Information) to external LLMs. This includes:
1.  **Metadata**: Table names (`patients`), Field names (`ssn`, `diagnosis`).
2.  **Dataset Values**: Literal values in queries (`"Diabetes"`, `"John Doe"`, `"123-45-6789"`).

However, LLMs rely on semantic context (meaningful names and values) to generate correct SQL/Script logic. Complete hashing (e.g., `TABLE_A` JOIN `TABLE_B` ON `FIELD_1`=`FIELD_2`) degrades the LLM's ability to infer join relationships, filter logic, and business rules, often leading to incorrect or hallucinatory code.

## 2. Proposed Solution: Semantic Aliasing & Value Tokenization

We propose a multi-layered obfuscation strategy that preserves **semantic meaning** (concepts) while hiding **specific identifiers** (raw names/values).

### A. Metadata Obfuscation (Tables & Fields)
Instead of purely random hashes, we replace raw names with **Synthetic Semantic Aliases** or provide a **Data Dictionary** via the prompt.

#### Strategy: The "Concept Map"
We maintain a mapping locally:
- **Raw**: `patients`
- **Obfuscated**: `STORE_8f9a2`
- **Description/Concept**: "A registry of individuals receiving care."

**Prompt Injection:**
Instead of sending:
```json
{ "name": "STORE_8f9a2", "fields": ["FIELD_x", "FIELD_y"] }
```
We send:
```text
Table: STORE_8f9a2 (Concept: Patients/Subjects)
Fields:
  - FIELD_1 (Concept: Unique Identifier, Type: UUID)
  - FIELD_2 (Concept: Full Name, Type: String)
  - FIELD_3 (Concept: Medical Condition/Diagnosis, Type: String)
```
*Note: The "Concept" descriptions must be generic enough not to leak PII but specific enough for the LLM to know "FIELD_3" is what we filter on for "Diabetes".*

### B. Value Obfuscation (Literals in Prompts)
When a user asks: *"Show me all patients with Diabetes."*
The word "Diabetes" is PHI (Diagnosis).

#### Strategy: Local Named Entity Recognition (NER) & Tokenization
Before sending the prompt to the LLM, a local NLP pre-processor (e.g., simple regex, dictionary lookup, or a small local BERT model) identifies potential PII entities.

1.  **User Prompt**: "Show me patients with Diabetes."
2.  **Local Pre-process**:
    - Identify "Diabetes" -> Sensitive Term.
    - Generate Token: `VAL_DIAGNOSIS_1`.
    - Map: `VAL_DIAGNOSIS_1` = `Diabetes`.
3.  **LLM Prompt**:
    "Write a script to select from `STORE_8f9a2` (Patients) where `FIELD_3` (Diagnosis) equals `VAL_DIAGNOSIS_1`."
4.  **LLM Output**:
    ```json
    { "op": "select", "args": { "store": "STORE_8f9a2", "filter": { "FIELD_3": "VAL_DIAGNOSIS_1" } } }
    ```
5.  **Local Execution**:
    - The Agent intercepts the script.
    - De-obfuscates `STORE_8f9a2` -> `patients`.
    - De-obfuscates `FIELD_3` -> `diagnosis`.
    - De-obfuscates `VAL_DIAGNOSIS_1` -> `Diabetes`.
    - Executes: `select * from patients where diagnosis = 'Diabetes'`.

## 3. Implementation Plan

### Phase 1: Enhanced Metadata Registry
1.  Update `StoreInfo` or a separate `PrivacyConfig` to hold "Safe Descriptions" for stores and fields.
2.  Update `DataAdminAgent` to inject these descriptions into the System Prompt alongside the obfuscated keys.

### Phase 2: Input Tokenizer (The "Sanitizer")
1.  Implement `Sanitizer` struct in `ai/agent`.
2.  Add `RegisterSensitiveTerm(term string, category string)` method.
3.  Integrate into `RunLoop`:
    - `sanitizedPrompt, tokenMap := sanitizer.Sanitize(userPrompt)`
    - Send `sanitizedPrompt` to LLM.
    - Store `tokenMap` in `ScriptContext`.

### Phase 3: Recursive De-obfuscation
1.  (Done) We already patched `deobfuscateMap` to handle keys.
2.  Extend `deobfuscateValue` to check the `tokenMap` from the sanitizer to restore literal values.

## 4. Example Transformation

**Original**:
`SELECT * FROM patients WHERE ssn = '123-45-6789'`

**Sanitized (Sent to LLM)**:
`SELECT * FROM STORE_PATIENTS WHERE FIELD_ID = 'VAL_PII_1'`
*(Context provided: STORE_PATIENTS is "User Registry", FIELD_ID is "Government ID")*

**LLM Generation**:
`{ "op": "select", "table": "STORE_PATIENTS", "where": {"FIELD_ID": "VAL_PII_1"} }`

**Executed**:
`SELECT * FROM patients WHERE ssn = '123-45-6789'`

## 5. Configuration & Performance ("On/Off" Switch)

To satisfy the requirement that this feature be optional (for performance or lack of need):

### Configuration Strategy
We will extend the `EnableObfuscation` flag in `ai/agent/config.go` or introduce a granular `ObfuscationConfig`:

```go
type ObfuscationConfig struct {
    Enabled bool   `json:"enabled"` // Master Switch
    Mode    string `json:"mode"`    // "metadata" (default), "full" (includes content/PII)
}
```

- **Off (`Enabled: false`)**:
  - Zero overhead.
  - Prompts use raw table names and values.
  - Fastest execution path.
  
- **Metadata Only (`Mode: "metadata"`)**:
  - Hushes table/store names and field definitions.
  - Leaves literal values in prompts as-is (e.g. "Diabetes").
  - Good for proprietary schema protection where data content is less sensitive.

- **Full / PHI (`Mode: "full"`)**:
  - **Highest Security**.
  - Obfuscates Metadata + Tokenizes Literal Values (PII/PHI).
  - Uses the Local Sanitizer key/value lookup.
  - Slight performance cost due to pre-processing (sanitization) and post-processing (de-obfuscation).

## 6. Security Guarantee
The LLM never sees the raw table names, raw field names, or raw literal values. It only sees structural tokens and generic semantic descriptions provided by the strictly controlled local configuration.
