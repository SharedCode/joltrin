import os
from PIL import Image, ImageDraw, ImageFont

WIDTH = 800
HEIGHT = 420
BG_COLOR = (9, 13, 21)
BORDER_COLOR = (30, 41, 59)
CARD_BG = (14, 21, 35)
BRAND_EMERALD = (16, 185, 129)
BRAND_CYAN = (6, 182, 212)
BRAND_BLUE = (79, 172, 254)
BRAND_VIOLET = (139, 92, 246)
BRAND_ROSE = (244, 63, 94)
BRAND_AMBER = (245, 158, 11)
TEXT_WHITE = (255, 255, 255)
TEXT_MUTED = (148, 163, 184)
TEXT_DIM = (100, 116, 139)

def get_font(size, bold=False):
    font_paths = [
        "/System/Library/Fonts/SFProText-Bold.otf" if bold else "/System/Library/Fonts/SFProText-Regular.otf",
        "/System/Library/Fonts/HelveticaNeue.ttc",
        "/System/Library/Fonts/Supplemental/Arial.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf" if bold else "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf"
    ]
    for p in font_paths:
        if os.path.exists(p):
            try:
                return ImageFont.truetype(p, size)
            except:
                pass
    return ImageFont.load_default()

font_title = get_font(26, bold=True)
font_subtitle = get_font(15, bold=True)
font_body = get_font(13, bold=False)
font_mono = get_font(12, bold=True)
font_small = get_font(10, bold=False)

def create_base_canvas():
    img = Image.new('RGB', (WIDTH, HEIGHT), BG_COLOR)
    draw = ImageDraw.Draw(img)
    # Subtle grid lines
    for x in range(0, WIDTH, 32):
        draw.line([(x, 0), (x, HEIGHT)], fill=(16, 23, 38))
    for y in range(0, HEIGHT, 32):
        draw.line([(0, y), (WIDTH, y)], fill=(16, 23, 38))
    # Border
    draw.rounded_rectangle([(4, 4), (WIDTH-5, HEIGHT-5)], radius=16, outline=BORDER_COLOR, width=2)
    # Top bar
    draw.rectangle([(5, 5), (WIDTH-6, 40)], fill=(14, 21, 35))
    draw.line([(5, 40), (WIDTH-6, 40)], fill=BORDER_COLOR, width=1)
    
    # Window dots
    draw.ellipse([(18, 18), (28, 28)], fill=(239, 68, 68))
    draw.ellipse([(36, 18), (46, 28)], fill=(234, 179, 8))
    draw.ellipse([(54, 18), (64, 28)], fill=(34, 197, 94))
    
    # Mini J brand squircle in top right
    zx = WIDTH - 52
    zy = 15
    draw.rounded_rectangle([(zx-4, zy-2), (zx+24, zy+20)], radius=4, fill=(11, 15, 25), outline=BRAND_CYAN, width=1)
    draw.line([(zx+12, zy+2), (zx+18, zy+2)], fill=BRAND_CYAN, width=2)
    draw.line([(zx+18, zy+2), (zx+18, zy+16)], fill=BRAND_BLUE, width=2)
    draw.line([(zx+18, zy+16), (zx+6, zy+16)], fill=BRAND_VIOLET, width=2)
    draw.line([(zx+6, zy+16), (zx+6, zy+12)], fill=BRAND_VIOLET, width=2)
    
    # Header tag
    draw.text((80, 16), "SHAREDCODE JOLTRIN // IN-PROCESS STATE ENGINE", fill=TEXT_MUTED, font=font_mono)
    return img, draw

frames = []

# --- SCENE 1: Intro & Architecture (12 frames) ---
for i in range(12):
    img, draw = create_base_canvas()
    
    # Title badge
    draw.rounded_rectangle([(60, 68), (275, 95)], radius=8, fill=(12, 32, 36), outline=BRAND_CYAN, width=1)
    draw.text((74, 74), "// JOLTRIN ARCHITECTURE", fill=BRAND_CYAN, font=font_mono)
    
    # Main Headline
    draw.text((60, 110), "One engine for data, memory, and compute.", fill=TEXT_WHITE, font=font_title)
    draw.text((60, 148), "Zero-server embedded state engine eliminates the multi-component database tax.", fill=TEXT_MUTED, font=font_body)
    
    # 3 Pill Cards
    cards = [
        ("ACID B-Tree", "Embedded storage kernel", BRAND_CYAN),
        ("Swarm Compute", "Distributed work queues", BRAND_EMERALD),
        ("Erasure Coding", "Zero-loss fault tolerance", BRAND_VIOLET)
    ]
    for idx, (title, desc, color) in enumerate(cards):
        cx = 60 + idx * 230
        draw.rounded_rectangle([(cx, 190), (cx + 215, 275)], radius=10, fill=CARD_BG, outline=color, width=1)
        draw.text((cx + 16, 208), title, fill=color, font=font_subtitle)
        draw.text((cx + 16, 238), desc, fill=TEXT_MUTED, font=font_small)
        
    # Footer comparison
    draw.text((60, 325), "WITHOUT JOLTRIN: App -> DB + Queue + Locks + Retries + Failover (6+ layers)", fill=BRAND_ROSE, font=font_mono)
    draw.text((60, 355), "WITH JOLTRIN:    App -> In-Process Engine (<0.3ms latency, 0 glue code)", fill=BRAND_EMERALD, font=font_mono)
    
    frames.append(img)

# --- SCENE 2: Demo 1: Client-Side WebAssembly (12 frames) ---
for i in range(12):
    img, draw = create_base_canvas()
    
    draw.rounded_rectangle([(60, 65), (310, 92)], radius=8, fill=(12, 32, 36), outline=BRAND_CYAN, width=1)
    draw.text((74, 71), "DEMO 1: CLIENT-SIDE WASM ENGINE", fill=BRAND_CYAN, font=font_mono)
    
    draw.text((60, 105), "Running Go Storage Kernel Directly in Browser", fill=TEXT_WHITE, font=font_title)
    
    # Terminal display
    draw.rounded_rectangle([(60, 145), (740, 385)], radius=10, fill=(5, 8, 14), outline=BORDER_COLOR, width=1)
    draw.text((80, 165), ">>> [Joltrin WASM] Initialized Transaction TX-000842 (Browser V8 Sandbox)", fill=BRAND_CYAN, font=font_mono)
    draw.text((80, 192), ">>> [ACID Isolation] Acquired B-Tree latch in Snapshot Isolation mode", fill=TEXT_MUTED, font=font_mono)
    draw.text((80, 219), ">>> [Mutation] Debited $250,000.00 from acc:001 (Acme Treasury)", fill=BRAND_VIOLET, font=font_mono)
    draw.text((80, 246), ">>> [Mutation] Credited $250,000.00 to acc:002 (Starlight Fund)", fill=BRAND_VIOLET, font=font_mono)
    draw.text((80, 273), ">>> [Consistency] Invariant check SUM(balances) == $17,500,000.00 PASSED", fill=BRAND_EMERALD, font=font_mono)
    draw.text((80, 300), ">>> [Atomic Commit] WAL flushed to local segment in 118 us (0.118 ms)", fill=BRAND_EMERALD, font=font_mono)
    draw.text((80, 335), "[OK] 0 HTTP Requests | 0 Database Servers | Microsecond In-Process Execution", fill=TEXT_WHITE, font=font_mono)
    
    frames.append(img)

# --- SCENE 3: Demo 2: Joltrin Arena Topology (12 frames) ---
for i in range(12):
    img, draw = create_base_canvas()
    
    draw.rounded_rectangle([(60, 65), (295, 92)], radius=8, fill=(24, 18, 42), outline=BRAND_VIOLET, width=1)
    draw.text((74, 71), "DEMO 2: JOLTRIN ARENA SIMULATOR", fill=BRAND_VIOLET, font=font_mono)
    
    draw.text((60, 105), "Live Distributed Systems Survival Mission", fill=TEXT_WHITE, font=font_title)
    
    # Topology Canvas Box
    draw.rounded_rectangle([(60, 145), (740, 385)], radius=10, fill=(5, 8, 14), outline=BORDER_COLOR, width=1)
    
    # Ingestion
    draw.rounded_rectangle([(85, 210), (195, 270)], radius=8, fill=CARD_BG, outline=BRAND_CYAN, width=1)
    draw.text((100, 224), "Ingestion API", fill=TEXT_WHITE, font=font_mono)
    draw.text((100, 246), "45,000 TPS", fill=BRAND_CYAN, font=font_small)
    
    # Coordinator
    draw.rounded_rectangle([(260, 195), (415, 285)], radius=10, fill=(16, 28, 48), outline=BRAND_CYAN, width=2)
    draw.text((275, 212), "JOLTRIN KERNEL", fill=BRAND_CYAN, font=font_subtitle)
    draw.text((275, 238), "OCC Coordinator", fill=TEXT_MUTED, font=font_small)
    draw.text((275, 258), "0-Master Swarm", fill=TEXT_MUTED, font=font_small)
    
    # Workers
    draw.rounded_rectangle([(475, 165), (585, 210)], radius=6, fill=CARD_BG, outline=BRAND_EMERALD, width=1)
    draw.text((487, 180), "Worker 01 [OK]", fill=BRAND_EMERALD, font=font_small)
    
    draw.rounded_rectangle([(475, 225), (585, 270)], radius=6, fill=CARD_BG, outline=BRAND_EMERALD, width=1)
    draw.text((487, 240), "Worker 02 [OK]", fill=BRAND_EMERALD, font=font_small)
    
    draw.rounded_rectangle([(475, 285), (585, 330)], radius=6, fill=CARD_BG, outline=BRAND_EMERALD, width=1)
    draw.text((487, 300), "Worker 03 [OK]", fill=BRAND_EMERALD, font=font_small)
    
    # Storage
    draw.rounded_rectangle([(630, 190), (725, 290)], radius=8, fill=CARD_BG, outline=BRAND_VIOLET, width=1)
    draw.text((642, 210), "B-Tree", fill=BRAND_VIOLET, font=font_mono)
    draw.text((642, 230), "Shard 1-4", fill=TEXT_MUTED, font=font_small)
    draw.text((642, 250), "Erasure", fill=TEXT_MUTED, font=font_small)
    draw.text((642, 268), "Parity OK", fill=BRAND_EMERALD, font=font_small)
    
    # Lines
    draw.line([(195, 240), (260, 240)], fill=BRAND_CYAN, width=2)
    draw.line([(415, 218), (475, 188)], fill=BRAND_EMERALD, width=1)
    draw.line([(415, 240), (475, 248)], fill=BRAND_EMERALD, width=1)
    draw.line([(415, 262), (475, 308)], fill=BRAND_EMERALD, width=1)
    draw.line([(585, 240), (630, 240)], fill=BRAND_VIOLET, width=1)
    
    frames.append(img)

# --- SCENE 4: Chaos & Failure Injected (10 frames) ---
for i in range(10):
    img, draw = create_base_canvas()
    
    draw.rounded_rectangle([(60, 65), (280, 92)], radius=8, fill=(35, 14, 20), outline=BRAND_ROSE, width=1)
    draw.text((74, 71), "CHAOS TEST: HARDWARE FAULT", fill=BRAND_ROSE, font=font_mono)
    
    draw.text((60, 105), "Simulating Node Crashes & Concurrent Storm", fill=TEXT_WHITE, font=font_title)
    
    draw.rounded_rectangle([(60, 145), (740, 385)], radius=10, fill=(22, 10, 15), outline=BRAND_ROSE, width=2)
    
    draw.text((85, 180), "[FAULT] STORAGE: Storage Shard #02 disk array offline!", fill=BRAND_ROSE, font=font_subtitle)
    draw.text((85, 220), "[CRASH] WORKER: Agent Worker 03 terminated abruptly mid-transaction.", fill=BRAND_AMBER, font=font_subtitle)
    draw.text((85, 260), "[CHAOS] STORM: 75,000 TPS concurrent write collision.", fill=BRAND_ROSE, font=font_subtitle)
    
    draw.rounded_rectangle([(85, 305), (715, 355)], radius=8, fill=CARD_BG, outline=BORDER_COLOR, width=1)
    draw.text((105, 322), "Traditional Stack: Split-brain deadlock, lost tasks, cascading 500 errors", fill=TEXT_MUTED, font=font_mono)
    
    frames.append(img)

# --- SCENE 5: Joltrin Autonomous Self-Healing (12 frames) ---
for i in range(12):
    img, draw = create_base_canvas()
    
    draw.rounded_rectangle([(60, 65), (295, 92)], radius=8, fill=(12, 34, 24), outline=BRAND_EMERALD, width=1)
    draw.text((74, 71), "AUTONOMOUS SELF-HEALING", fill=BRAND_EMERALD, font=font_mono)
    
    draw.text((60, 105), "Zero-Loss Erasure Coding & Swarm Redistribution", fill=TEXT_WHITE, font=font_title)
    
    draw.rounded_rectangle([(60, 145), (740, 385)], radius=10, fill=(8, 24, 18), outline=BRAND_EMERALD, width=2)
    
    draw.text((85, 175), "[PASS] REED-SOLOMON RECONSTRUCTION: Missing data blocks rebuilt in-memory", fill=BRAND_EMERALD, font=font_subtitle)
    draw.text((85, 215), "[PASS] HEARTBEAT FAILOVER: 847 queued tasks redistributed to healthy workers in 12ms", fill=BRAND_CYAN, font=font_subtitle)
    draw.text((85, 255), "[PASS] OCC SERIALIZATION: 0 lock corruptions across concurrent transaction storm", fill=BRAND_EMERALD, font=font_subtitle)
    
    draw.rounded_rectangle([(85, 305), (715, 355)], radius=8, fill=CARD_BG, outline=BRAND_EMERALD, width=1)
    draw.text((105, 322), "SYSTEM SURVIVED: 100.00% ACID Consistency | 0 Dropped Writes", fill=TEXT_WHITE, font=font_mono)
    
    frames.append(img)

# --- SCENE 6: Demo 3: AI Agent Verification Barrier (12 frames) ---
for i in range(12):
    img, draw = create_base_canvas()
    
    draw.rounded_rectangle([(60, 65), (315, 92)], radius=8, fill=(12, 32, 36), outline=BRAND_CYAN, width=1)
    draw.text((74, 71), "DEMO 3: AGENT VERIFICATION BARRIER", fill=BRAND_CYAN, font=font_mono)
    
    draw.text((60, 105), "ai/verify Barrier Gating Real Runbooks in WASM", fill=TEXT_WHITE, font=font_title)
    
    draw.rounded_rectangle([(60, 145), (740, 385)], radius=10, fill=(5, 8, 14), outline=BORDER_COLOR, width=1)
    
    draw.text((80, 168), "BARRIER CERTIFICATE: Mathematical safety invariants before state mutation", fill=BRAND_CYAN, font=font_mono)
    draw.text((80, 202), "* Agent executes: execute_step(drop_prod_db) ... BLOCKED (Precondition not met)", fill=BRAND_ROSE, font=font_mono)
    draw.text((80, 232), "* Agent executes: execute_step(take_backup)   ... COMMITTED (State: backup_taken)", fill=BRAND_EMERALD, font=font_mono)
    draw.text((80, 262), "* Agent executes: execute_step(validate_backup) ... COMMITTED (State: validated)", fill=BRAND_EMERALD, font=font_mono)
    draw.text((80, 292), "* Agent executes: execute_step(drop_prod_db) ... ALLOWED (Invariants satisfied)", fill=BRAND_EMERALD, font=font_mono)
    
    draw.text((80, 335), "[PASS] Identical engine gating tools/mcpserver and tools/a2aagent in production", fill=TEXT_WHITE, font=font_mono)
    
    frames.append(img)

# --- SCENE 7: Summary & Demos (14 frames) ---
for i in range(14):
    img, draw = create_base_canvas()
    
    draw.rounded_rectangle([(60, 65), (280, 92)], radius=8, fill=(24, 18, 42), outline=BRAND_VIOLET, width=1)
    draw.text((74, 71), "REAL-TIME AI AGENT SWARMS", fill=BRAND_VIOLET, font=font_mono)
    
    draw.text((60, 105), "Persistent State for Distributed AI Workloads", fill=TEXT_WHITE, font=font_title)
    
    draw.rounded_rectangle([(60, 145), (740, 385)], radius=10, fill=CARD_BG, outline=BORDER_COLOR, width=1)
    
    draw.text((85, 175), "DATA + COMPUTE + COORDINATION + TRANSACTIONS -> JOLTRIN", fill=BRAND_CYAN, font=font_subtitle)
    draw.text((85, 210), "* Persistent Context: AI agent reasoning buffers commit atomically in <15ms", fill=TEXT_WHITE, font=font_body)
    draw.text((85, 240), "* Vector Search: 128-d cosine similarity evaluated locally in memory", fill=TEXT_WHITE, font=font_body)
    draw.text((85, 270), "* Resilient Swarm: Workers join and leave without orphan locks or dropped tasks", fill=TEXT_WHITE, font=font_body)
    draw.text((85, 315), "Experience all 3 live interactive experiences on GitHub Pages:", fill=BRAND_CYAN, font=font_mono)
    draw.text((85, 342), "sharedcode.github.io/joltrin  |  /arena  |  /agents", fill=TEXT_WHITE, font=font_mono)
    
    frames.append(img)

# Save as optimized GIF to docs/assets/sop-demo.gif and docs/assets/joltrin-demo.gif
out_paths = ["docs/assets/sop-demo.gif", "docs/assets/joltrin-demo.gif"]
for p in out_paths:
    frames[0].save(
        p,
        save_all=True,
        append_images=frames[1:],
        duration=250,
        loop=0,
        optimize=True
    )
    print(f"✓ Successfully generated {p} ({os.path.getsize(p)} bytes, {len(frames)} frames)")
