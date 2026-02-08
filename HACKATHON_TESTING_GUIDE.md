# The Great Stories — Hackathon Testing Guide

**Welcome, testers!** This guide will walk you through testing our AI-powered content enrichment system that transforms plain text into immersive, multimedia stories with images and audio narration.

---

## 🎯 Quick Start (5 minutes)

### Option 1: Web Interface (Easiest)
1. **Open**: https://stories.snappyloop.work
2. **Get API Key**: Email vasily.kulakov@gmail.com for instant access
3. **Navigate to "Generation"** page
4. **Try the demo** (see [Example 1](#example-1-educational-story) below)

### Option 2: API Testing
Use `curl` or any HTTP client with the examples in [API Testing](#api-testing-detailed).

---

## 📋 What to Test

### ⚠️ Text Length Requirements
**Important**: The system requires a minimum text length for meaningful segmentation:
- **Minimum**: 300-500 characters (roughly 2-3 paragraphs)
- **Very short texts** (1-2 sentences) will NOT be segmented, even if you request multiple segments
- **Optimal**: 500-2000 words for best demonstration of features
- All examples below use properly sized texts for successful segmentation

### Core Functionality
- ✅ Text segmentation with intelligent boundaries
- ✅ Image generation for each segment
- ✅ Audio narration (free speech & podcast styles)
- ✅ Multi-modal input (upload PDFs or images)
- ✅ Fact-checking integration
- ✅ Job status tracking and webhooks
- ✅ Three content types: Educational, Financial, Fictional

### Advanced Features
- ✅ MCP protocol support (agents as tools)
- ✅ WebSocket real-time streaming
- ✅ Configurable segment counts (1-20)
- ✅ Fallback mechanisms for robustness

---

## 🌟 Testing Scenarios

### Example 1: Educational Story

**Goal**: Create an engaging educational story with diagrams and clear narration.

**Web Interface**:
1. Go to https://stories.snappyloop.work/generation
2. Enter your API key
3. Paste this text:
   ```
   The solar system is a gravitationally bound system consisting of the Sun and all the objects that orbit it. At the center lies our star, the Sun, which contains 99.86% of the system's total mass and provides the energy that sustains life on Earth through nuclear fusion in its core.
   
   The inner solar system is home to four rocky planets called terrestrial planets. Mercury, the smallest and closest planet to the Sun, experiences the most extreme temperature variations in the solar system, ranging from 430°C during the day to -180°C at night due to its lack of atmosphere. Venus, Earth's sister planet, is shrouded in a thick atmosphere of carbon dioxide that creates a runaway greenhouse effect, making it the hottest planet with surface temperatures reaching 465°C. Earth, the third planet, is unique in having liquid water on its surface and is the only known world to harbor life. Mars, often called the Red Planet due to iron oxide on its surface, once had flowing water and may have harbored microbial life in its ancient past.
   
   Beyond Mars lies the asteroid belt, a region containing millions of rocky remnants from the solar system's formation. The outer solar system is dominated by four gas and ice giants. Jupiter, the largest planet, has a mass greater than all other planets combined and features the Great Red Spot, a storm larger than Earth that has raged for centuries. Saturn is famous for its spectacular ring system made of ice and rock particles. Uranus rotates on its side, likely due to a massive collision early in its history. Neptune, the windiest planet, has storms with speeds reaching 2,000 kilometers per hour. The solar system extends far beyond Neptune to include the Kuiper Belt and the distant Oort Cloud, regions filled with icy bodies and the source of many comets.
   ```
4. Set:
   - **Type**: Educational
   - **Segments**: 3
   - **Audio**: Free speech
   - **Fact-check**: ✓ (enabled)
5. Click **"Send request"**
6. Copy the `job_id` and click **"Get job"** to monitor progress
7. When complete, click **"View"** to see the final result

**What to verify**:
- ✓ Text is split into logical segments with descriptive titles
- ✓ Each segment has a relevant, educational-style image
- ✓ Audio narration is clear and conversational
- ✓ Fact-check results appear (if enabled)
- ✓ Processing completes in ~2-5 minutes

**API Version**:
```bash
curl -X POST https://stories.snappyloop.work/v1/jobs \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "The solar system is a gravitationally bound system consisting of the Sun and all the objects that orbit it...[full text above]",
    "type": "educational",
    "segments_count": 3,
    "audio_type": "free_speech",
    "fact_check_needed": true
  }'
```

**⚠️ Important**: Text must be at least **300-500 characters** for segmentation to work properly. Very short texts (1-2 sentences) will not be segmented even if you request multiple segments, as there's not enough content to divide meaningfully.

---

### Example 2: Financial Content with Podcast Style

**Goal**: Professional narration with disclaimers and restrained visuals.

**Web Interface**:
1. Navigate to https://stories.snappyloop.work/generation
2. Paste:
   ```
   Understanding stock market fundamentals is crucial for long-term investment success. Stock markets serve as platforms where publicly traded companies can raise capital while providing investors with opportunities to build wealth over time. These markets function through a complex system of exchanges, brokers, and electronic trading platforms that facilitate billions of transactions daily.
   
   Portfolio diversification is one of the most important principles in investment management. By spreading investments across different asset classes such as stocks, bonds, real estate, and commodities, investors can reduce the overall risk of their portfolio. This strategy is based on the principle that different asset classes often perform differently under various economic conditions. When stocks are underperforming, bonds might provide stability, and vice versa. Modern portfolio theory, developed by Harry Markowitz in the 1950s, mathematically demonstrates how diversification can optimize returns for a given level of risk.
   
   Dollar-cost averaging is an investment strategy where an investor divides the total amount to be invested across periodic purchases of a target asset. This approach reduces the impact of volatility by spreading purchases over time. For example, instead of investing $12,000 all at once, an investor might invest $1,000 per month for twelve months. This means they buy more shares when prices are low and fewer when prices are high, potentially lowering the average cost per share over time. This strategy is particularly popular for retirement accounts and can help investors avoid the psychological pitfalls of trying to time the market, which even professional investors find extremely difficult to do consistently.
   ```
3. Set:
   - **Type**: Financial
   - **Segments**: 3
   - **Audio**: Podcast (⭐ professional narration style)
   - **Fact-check**: ✓
4. Submit and monitor

**What to verify**:
- ✓ Audio has professional podcast pacing and tone
- ✓ Financial disclaimers appear in narration
- ✓ Images are professional, not flashy or misleading
- ✓ Content maintains measured, conservative tone

---

### Example 3: Fictional Story with Atmosphere

**Goal**: Cinematic imagery and dramatic narration.

**Web Interface**:
1. Use this text:
   ```
   The old lighthouse stood alone on the rocky cliff, its weathered stones bearing witness to a hundred years of storms. Waves crashed against the base with a fury that sent spray fifty feet into the air, and the wind howled through the cracks in the tower like the voices of lost sailors. Inside, Thomas kept his solitary vigil as he had every night for the past twenty years, his weathered hands moving with practiced precision as he tended to the massive Fresnel lens.
   
   Through the salt-stained windows, he could see the storm approaching from the east, a wall of darkness advancing across the grey ocean. The barometer had been falling all day, and now the first rumbles of thunder echoed across the water. Thomas knew this night would test him. Ships would be seeking harbor, their crews desperate for the guiding beam that meant safety and home. He checked his logs, noted the time, and began his preparations with the methodical calm of a man who had faced nature's fury many times before.
   
   As darkness fell, the storm arrived with terrible force. Rain lashed the lighthouse in horizontal sheets, and lightning illuminated the world in brief, stark flashes that left afterimages burned into Thomas's vision. The tower swayed—it always did in storms this fierce—but Thomas felt no fear. He climbed the spiral staircase, his boots ringing on the iron steps, ascending through the stone cylinder until he reached the lamp room. There, surrounded by glass and brass and the steady turning of the lens, he stood watch. Through breaks in the storm, he glimpsed lights on the horizon—ships, fighting their way to port, following his beacon through the chaos. This was his purpose, his duty, his life. As long as the light burned, sailors had hope, and Thomas would never let that light go dark.
   ```
2. Set:
   - **Type**: Fictional
   - **Segments**: 3
   - **Audio**: Free speech
3. Submit

**What to verify**:
- ✓ Images capture mood and atmosphere
- ✓ Narration is immersive and dramatic
- ✓ Visual style is cinematic

---

### Example 4: Multi-Modal Input (PDF/Image Upload)

**Goal**: Test document and image processing with text extraction.

**Web Interface**:
1. Go to https://stories.snappyloop.work/generation
2. **Skip the text field** or add minimal text
3. Click **"Choose Files"** and select:
   - A PDF document (article, report, etc.)
   - OR images with text (photos, screenshots, diagrams)
4. Set preferences (type, segments, audio)
5. Submit

**What to verify**:
- ✓ Files upload successfully
- ✓ Text is extracted from PDFs/images automatically
- ✓ Extracted text appears in job details
- ✓ Full pipeline runs on extracted content
- ✓ Can combine uploaded files + manual text

**Note**: Files with copyrighted content may be rejected by content policy.

---

### Example 5: High Segment Count

**Goal**: Test scalability with many segments.

**Web Interface**:
1. Use a longer text (this example is ~800 words):
   ```
   The history of artificial intelligence is a fascinating journey that spans several decades and represents humanity's quest to create machines that can think and learn. The concept of artificial beings with intelligence has roots in ancient mythology, but the modern field of AI began in earnest during the mid-20th century.
   
   The birth of AI as an academic discipline is often dated to the Dartmouth Conference in 1956, where John McCarthy, Marvin Minsky, Claude Shannon, and others gathered to discuss the possibility of creating intelligent machines. McCarthy coined the term "artificial intelligence" for this conference, defining it as the science and engineering of making intelligent machines. The attendees were optimistic, believing that machines matching human intelligence were just around the corner. This period saw the development of early AI programs that could play checkers, prove mathematical theorems, and solve algebra word problems.
   
   The 1960s and 1970s brought both progress and challenges. Researchers developed new programming languages like LISP, specifically designed for AI research. Expert systems emerged, capturing the knowledge of human experts in specific domains. DENDRAL could identify molecular structures, while MYCIN could diagnose blood infections and recommend treatments. However, limitations in computing power and the complexity of real-world problems led to what became known as the "AI Winter"—periods of reduced funding and interest in AI research.
   
   The 1980s saw a resurgence with the commercial success of expert systems and the development of neural networks. Companies invested heavily in AI, and Japan launched the ambitious Fifth Generation Computer Project. However, another AI Winter followed when many of these projects failed to deliver on their promises. The limitations of rule-based systems became apparent, and the computational requirements for neural networks exceeded available hardware capabilities.
   
   The late 1990s and early 2000s marked a turning point. Increases in computational power, the availability of large datasets, and improvements in algorithms led to practical AI applications. IBM's Deep Blue defeated world chess champion Garry Kasparov in 1997, demonstrating that machines could excel at complex strategic thinking. Machine learning, particularly neural networks, began to show impressive results in pattern recognition and data analysis.
   
   The current era, beginning around 2010, has witnessed explosive growth in AI capabilities. Deep learning, enabled by powerful GPUs and vast amounts of training data, has revolutionized computer vision, natural language processing, and speech recognition. AlphaGo's victory over the world Go champion in 2016 demonstrated AI's ability to master intuitive, strategic games previously thought to require uniquely human skills. Large language models like GPT have shown remarkable abilities in understanding and generating human-like text. AI systems now power virtual assistants, recommendation engines, autonomous vehicles, medical diagnosis tools, and countless other applications that affect daily life.
   
   Today, AI research focuses on making systems more robust, interpretable, and aligned with human values. Questions about AI ethics, fairness, and safety have become central to the field. Researchers are working on explainable AI to understand how models make decisions, on reducing bias in training data and algorithms, and on ensuring that as AI systems become more powerful, they remain beneficial to humanity. The field continues to evolve rapidly, with breakthroughs in areas like reinforcement learning, transfer learning, and multi-modal models that can process and generate different types of data simultaneously.
   ```
2. Set **Segments**: 7-10
3. Submit

**What to verify**:
- ✓ Segmentation creates natural boundaries (likely by historical period)
- ✓ All segments get images and audio
- ✓ No gaps or overlaps in text coverage
- ✓ Processing completes successfully (may take 5-10 minutes)
- ✓ Each segment has a descriptive title matching the time period/topic

---

## 🔧 API Testing (Detailed)

### Base URL
```
https://stories.snappyloop.work
```

### Authentication
All requests require:
```
Authorization: Bearer YOUR_API_KEY
```

### 1. Create a Job

**Request**:
```bash
curl -X POST https://stories.snappyloop.work/v1/jobs \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "text": "Your text here...",
    "type": "educational",
    "segments_count": 3,
    "audio_type": "free_speech",
    "fact_check_needed": false,
    "webhook": {
      "url": "https://your-webhook-endpoint.com/callback",
      "secret": "optional-secret-for-signing"
    }
  }'
```

**Response (202 Accepted)**:
```json
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "queued",
  "created_at": "2026-02-08T12:00:00Z"
}
```

### 2. Check Job Status

**Request**:
```bash
curl https://stories.snappyloop.work/v1/jobs/YOUR_JOB_ID \
  -H "Authorization: Bearer YOUR_API_KEY"
```

**Response** (when complete):
```json
{
  "job": {
    "id": "550e8400-...",
    "status": "succeeded",
    "input_type": "educational",
    "segments_count": 3,
    "audio_type": "free_speech"
  },
  "segments": [
    {
      "id": "...",
      "idx": 0,
      "title": "Introduction to Solar System",
      "segment_text": "...",
      "status": "succeeded"
    }
  ],
  "assets": [
    {
      "asset": {
        "id": "asset-id-1",
        "kind": "image",
        "mime_type": "image/png",
        "size_bytes": 245680
      },
      "download_url": "/v1/assets/asset-id-1/content"
    },
    {
      "asset": {
        "id": "asset-id-2",
        "kind": "audio",
        "mime_type": "audio/wav",
        "size_bytes": 1234567
      },
      "download_url": "/v1/assets/asset-id-2/content"
    }
  ],
  "files": []
}
```

### 3. List Your Jobs

```bash
curl https://stories.snappyloop.work/v1/jobs?limit=10 \
  -H "Authorization: Bearer YOUR_API_KEY"
```

### 4. Download Assets

Use the `download_url` from job response:
```bash
curl https://stories.snappyloop.work/v1/assets/ASSET_ID/content \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -o output.png
```

### 5. Upload Files First

**Upload a PDF or image**:
```bash
curl -X POST https://stories.snappyloop.work/v1/files \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -F "file=@document.pdf"
```

**Response**:
```json
{
  "file_id": "file-uuid",
  "filename": "document.pdf",
  "mime_type": "application/pdf",
  "size_bytes": 123456,
  "expires_at": "2026-02-15T12:00:00Z"
}
```

**Then create job with file**:
```bash
curl -X POST https://stories.snappyloop.work/v1/jobs \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "file_ids": ["file-uuid"],
    "type": "educational",
    "segments_count": 3,
    "audio_type": "podcast"
  }'
```

---

## 🤖 Advanced: MCP Protocol Testing

**What is MCP?** Model Context Protocol - allows the system to be used as AI agent tools.

**MCP Server**: https://rpc.snappyloop.work

### Available Tools via MCP:
- `segment_text` - Intelligent text segmentation
- `fact_check` - Verify facts in text
- `generate_image_prompt` - Create optimized image prompts
- `generate_image` - Generate images from prompts

### Test via Web Interface:
1. Go to https://stories.snappyloop.work/agents
2. Enter API key at the top
3. Select **"MCP"** transport
4. Try each agent form:
   - **Segmentation**: Test how text is split into logical parts
   - **Fact-check**: Verify claims in text
   - **Image prompt**: See optimized prompts for image generation
   - **Image**: Generate actual images

### Example MCP Test:
**Segmentation**:
- Text: 
  ```
  Quantum computing represents a revolutionary approach to computation that leverages the principles of quantum mechanics. Unlike classical computers that use bits representing either 0 or 1, quantum computers use quantum bits or qubits that can exist in a superposition of both states simultaneously. This fundamental difference allows quantum computers to explore multiple solution paths in parallel, potentially solving certain types of problems exponentially faster than classical computers. Applications include cryptography, drug discovery, financial modeling, and optimization problems that are intractable for conventional computers. However, building practical quantum computers faces significant challenges including maintaining quantum coherence and correcting errors caused by environmental interference.
  ```
- Segments: 3
- Type: Educational
- Click "Segment"

**What to verify**:
- ✓ Returns structured JSON with boundaries
- ✓ Segments have descriptive titles (e.g., "Introduction to Quantum Computing", "Quantum Advantages", "Practical Challenges")
- ✓ No gaps or overlaps in character ranges
- ✓ Natural breaks at sentence/topic boundaries

---

## 🔍 What Makes This Special

### Gemini 3 Integration Highlights

**Multiple Specialized Models**:
- `gemini-3.0-flash` - Fast text segmentation
- `gemini-3-pro-preview` - High-quality narration scripts
- `gemini-3-pro-image-preview` - **Native image generation** (no third-party APIs!)
- `gemini-2.5-pro-preview-tts` - **Native text-to-speech** with multiple voices

**Content-Aware Processing**:
- Educational content gets clear, instructional images and conversational tone
- Financial content includes disclaimers and professional visuals
- Fictional content has cinematic atmosphere and dramatic narration

**Robust Fallbacks**:
- If AI models fail, system uses deterministic fallbacks
- Jobs always complete successfully
- No silent failures

**End-to-End Automation**:
- Single API call → Full enriched story
- Async processing with webhooks
- All assets stored and accessible via API

---

## ⚡ Local Development Testing (Optional)

If you're running the service locally, you'll also have access to **gRPC agents**:

### Setup:
```bash
git clone <repository>
cd stories
cp env.example .env
# Add your GEMINI_API_KEY to .env
docker-compose up -d
```

### Access:
- API: http://localhost:8080
- Web UI: http://localhost:8080
- Agents: http://localhost:8080/agents (gRPC + MCP available)

### Test gRPC Agents:
1. Navigate to http://localhost:8080/agents
2. Select **"gRPC"** transport
3. Test **Narration** and **Audio (TTS)** forms (gRPC-only features)

---

## 🐛 Troubleshooting

### Job stays in "queued" status
- Workers may be processing other jobs
- Check back after 1-2 minutes
- If >5 minutes, contact support

### "Quota exceeded" error
- Contact vasily.kulakov@gmail.com for quota increase
- Each API key has character limits

### "Content policy violation"
- Some files (copyrighted books, comics) may be rejected
- Try different content or use plain text

### Assets not loading
- Download URLs expire after some time
- Re-fetch job details to get fresh URLs

### Fact-check not showing
- Only appears if `fact_check_needed: true`
- Check the `files` section in job response
- Fact-checks are per-segment

---

## 📊 Testing Checklist

Use this to ensure comprehensive testing:

### Basic Features
- [ ] Create job with text only
- [ ] Create job with PDF upload
- [ ] Create job with image upload
- [ ] Test all three content types (educational, financial, fictional)
- [ ] Test both audio styles (free_speech, podcast)
- [ ] Enable fact-checking
- [ ] Test different segment counts (1, 3, 10)

### Quality Checks
- [ ] Segments have natural boundaries (sentences/paragraphs)
- [ ] Segment titles are descriptive
- [ ] Images match segment content
- [ ] Audio is clear and appropriate tone
- [ ] No missing segments or assets
- [ ] Fact-check results are relevant

### API & Integration
- [ ] List jobs endpoint works
- [ ] Download assets successfully
- [ ] Webhook delivery (if configured)
- [ ] File upload and extraction
- [ ] Multiple jobs in parallel

### Advanced
- [ ] MCP agent tools (segmentation, fact-check, image)
- [ ] WebSocket streaming (agents page)
- [ ] Long text input (~10,000+ chars)

---

## 💡 Tips for Best Results

1. **Text Length**: 
   - **Minimum**: 300-500 characters for segmentation to work
   - **Optimal**: 500-2000 words for best results
   - **Too short**: 1-2 sentences won't be segmented even if you request multiple segments
   - **Too long**: Processing time increases (10-15 minutes for very long texts)

2. **Segment Count**: 3-5 segments is ideal for demo. Shows capability without excessive wait time.

3. **Content Type Matters**: 
   - Educational: Use science, history, how-to content
   - Financial: Use market analysis, investment concepts
   - Fictional: Use stories with vivid descriptions

4. **Podcast Style**: Most impressive for professional presentations. Adds emphasis and pacing.

5. **Fact-Checking**: Enable for educational/financial to show accuracy verification.

6. **Multi-Modal**: Upload a PDF article or image with text to showcase extraction capabilities.

---

## 📞 Support & Questions

**Email**: vasily.kulakov@gmail.com
**For**: API keys, quota increases, technical issues

**Documentation**:
- OpenAPI Spec: https://stories.snappyloop.work/openapi.yaml

---

## 🎉 Have Fun Testing!

This system demonstrates the power of Gemini 3's multimodal capabilities. From plain text to rich multimedia stories with a single API call. Try creative combinations, test edge cases, and see how the AI adapts to different content types!

**Quick test**: 30 seconds to create a job, 2-3 minutes to see results. Simple, powerful, impressive.
