# Autonomous Business Builder: Vision & Architecture

## Executive Summary

Kaggen's Business Builder is an autonomous agent orchestration system that can research, build, launch, and grow a profitable online business — end to end. It extends Kaggen's existing coordinator-team pattern from a single software development pipeline to a full business lifecycle managed by 12 specialized pipelines.

Each pipeline follows the same structural pattern: a sequence of role-specific agents that delegate work to Claude Code CLI, coordinate via beads (git-backed issue tracking), and produce artifacts in a shared project directory. A meta-orchestrator ("CEO agent") sequences pipelines, makes go/no-go decisions, and manages cross-pipeline dependencies.

The key insight: **every pipeline is structurally identical**. Only the roles, artifacts, and domain knowledge differ. This means the framework can be generalized — pipelines defined as YAML config rather than hardcoded Go — enabling rapid creation of new business domains without code changes.

---

## The 12 Pipelines

### 1. Market Validation

**Purpose:** Determine whether a viable market exists before building anything.

| Role | Description | Artifacts |
|------|-------------|-----------|
| Researcher | Analyzes market size, trends, and demand signals | `MARKET_RESEARCH.md` |
| Customer Analyst | Identifies target personas and pain points | `PERSONAS.md` |
| Validator | Designs and evaluates validation experiments (surveys, landing pages) | `VALIDATION_REPORT.md` |

**Beads integration:** Epic per market hypothesis. Issues for each research task and validation experiment. Go/no-go verdict stored as epic close reason.

**Why separate:** Building without market validation is the #1 startup failure mode. This pipeline produces a kill/proceed decision before any resources are committed.

---

### 2. Competitive Intelligence

**Purpose:** Map the competitive landscape and identify differentiation opportunities.

| Role | Description | Artifacts |
|------|-------------|-----------|
| Scout | Identifies direct and indirect competitors | `COMPETITORS.md` |
| Analyst | Evaluates competitor strengths, weaknesses, pricing, positioning | `COMPETITIVE_ANALYSIS.md` |
| Strategist | Recommends positioning and differentiation angles | `POSITIONING.md` |

**Beads integration:** Issues per competitor analyzed. Specs contain SWOT analysis. Dependencies link to Market Validation findings.

**Why separate:** Competitive analysis requires different skills (OSINT, product teardown) than market research, and the output directly feeds Design System and Marketing pipelines.

---

### 3. Legal & Compliance

**Purpose:** Ensure the business operates within legal boundaries from day one.

| Role | Description | Artifacts |
|------|-------------|-----------|
| Compliance Analyst | Identifies regulatory requirements (GDPR, PCI-DSS, etc.) | `COMPLIANCE_REQUIREMENTS.md` |
| Policy Drafter | Creates privacy policy, ToS, cookie policy | `legal/privacy-policy.md`, `legal/terms.md` |
| Reviewer | Validates all artifacts meet compliance requirements | `COMPLIANCE_REPORT.md` |

**Beads integration:** Issues per regulation/requirement. Child issues for each policy document. QA-style validation against a compliance checklist.

**Why separate:** Legal requirements constrain all other pipelines. Getting this wrong has severe consequences (fines, shutdown). Must run early and feed constraints to Design, Payment, and Content pipelines.

---

### 4. Design System

**Purpose:** Create a cohesive visual identity and component library.

| Role | Description | Artifacts |
|------|-------------|-----------|
| Brand Designer | Defines brand identity (colors, typography, voice) | `BRAND_GUIDE.md` |
| UX Architect | Creates information architecture and user flows | `USER_FLOWS.md`, wireframes |
| Component Designer | Designs reusable UI components | `DESIGN_SYSTEM.md`, component specs |

**Beads integration:** Epic for overall design system. Issues per component/page. Specs include responsive breakpoints and accessibility requirements.

**Why separate:** Design decisions cascade through Software Development, Content, and Marketing. Establishing the system first prevents inconsistency and rework.

---

### 5. Software Development (Existing)

**Purpose:** Build the product.

| Role | Description | Artifacts |
|------|-------------|-----------|
| Product Owner | Decomposes requirements into user stories with acceptance criteria | `BACKLOG.md`, beads epic |
| Architect | Produces technical specifications | `SPEC.md`, beads comments |
| Coder | Implements the code via Claude Code CLI | Source code, tests |
| QA | Validates against acceptance criteria | `QA_REPORT.md` |

**Beads integration:** Full lifecycle — epic creation, story decomposition, spec comments, implementation tracking, QA pass/fail with automated close.

**Why separate:** This is the core build pipeline. It already exists and is proven. Other pipelines feed requirements into it and consume its outputs.

---

### 6. Payment & Monetization

**Purpose:** Implement revenue generation — payment processing, pricing, subscriptions.

| Role | Description | Artifacts |
|------|-------------|-----------|
| Monetization Strategist | Defines pricing model, tiers, and revenue projections | `PRICING.md` |
| Payment Architect | Designs payment integration (Stripe, PayPal, etc.) | `PAYMENT_SPEC.md` |
| Implementer | Builds payment flows, webhook handlers, billing UI | Source code |
| Compliance Checker | Validates PCI-DSS compliance, receipt generation | `PAYMENT_COMPLIANCE.md` |

**Beads integration:** Issues for each payment flow (checkout, subscription, refund, invoice). Dependencies on Legal pipeline for compliance requirements and Software Development for API contracts.

**Why separate:** Payment is security-critical and regulation-heavy. Mistakes here lose money or break laws. Dedicated attention prevents "just bolt on Stripe" shortcuts.

---

### 7. DevOps / Infrastructure

**Purpose:** Deploy, monitor, and operate the product reliably.

| Role | Description | Artifacts |
|------|-------------|-----------|
| Infrastructure Architect | Designs deployment topology (cloud provider, regions, scaling) | `INFRASTRUCTURE.md` |
| DevOps Engineer | Creates CI/CD pipelines, Docker configs, IaC (Terraform/Pulumi) | `Dockerfile`, `docker-compose.yml`, CI configs |
| SRE | Sets up monitoring, alerting, logging, and incident response | `MONITORING.md`, alert configs |

**Beads integration:** Issues per infrastructure component. Dependencies on Software Development (what to deploy) and Payment (uptime SLAs).

**Why separate:** Infrastructure decisions (cloud vs. serverless, region selection, scaling strategy) are architectural choices that affect cost, performance, and reliability. They need dedicated analysis.

---

### 8. Content & SEO

**Purpose:** Create content that drives organic traffic and establishes authority.

| Role | Description | Artifacts |
|------|-------------|-----------|
| SEO Strategist | Performs keyword research and content gap analysis | `SEO_STRATEGY.md` |
| Content Planner | Creates editorial calendar and content briefs | `CONTENT_CALENDAR.md` |
| Writer | Produces blog posts, landing page copy, documentation | `content/` directory |
| SEO Auditor | Validates technical SEO (meta tags, schema, sitemap) | `SEO_AUDIT.md` |

**Beads integration:** Issues per content piece. Specs include target keywords, word count, internal linking requirements. Dependencies on Design System for brand voice.

**Why separate:** Content production is ongoing and follows a different cadence than software development. SEO requires specialized knowledge (keyword research, link building strategy) that doesn't belong in a coder's workflow.

---

### 9. Marketing Campaign

**Purpose:** Drive awareness and acquisition through targeted campaigns.

| Role | Description | Artifacts |
|------|-------------|-----------|
| Campaign Strategist | Defines channels, targeting, and budget allocation | `MARKETING_STRATEGY.md` |
| Copywriter | Creates ad copy, email sequences, social posts | `campaigns/` directory |
| Analytics Setup | Configures tracking pixels, UTM parameters, conversion goals | `TRACKING.md` |
| Campaign Analyst | Monitors performance and recommends optimizations | `CAMPAIGN_REPORT.md` |

**Beads integration:** Epic per campaign. Issues for each channel/creative variant. Dependencies on Content pipeline for landing pages and Design System for visual assets.

**Why separate:** Marketing requires rapid iteration (A/B testing, budget reallocation) on a different timeline than product development. Mixing marketing tasks with dev tasks creates noise in both.

---

### 10. Customer Support

**Purpose:** Handle customer inquiries and build support infrastructure.

| Role | Description | Artifacts |
|------|-------------|-----------|
| Knowledge Base Author | Creates FAQ, help articles, troubleshooting guides | `docs/help/` directory |
| Support Flow Designer | Designs chatbot flows, escalation paths, SLA policies | `SUPPORT_DESIGN.md` |
| Integrator | Implements support tooling (chatbot, ticketing, email templates) | Source code, configs |

**Beads integration:** Issues per help article and support flow. Dependencies on Software Development for product knowledge and Content for brand voice consistency.

**Why separate:** Support infrastructure needs to be ready at launch, not bolted on after the first angry customer. Proactive support design reduces churn.

---

### 11. Analytics & Growth

**Purpose:** Instrument the product for data-driven growth decisions.

| Role | Description | Artifacts |
|------|-------------|-----------|
| Analytics Architect | Defines event taxonomy and tracking plan | `TRACKING_PLAN.md` |
| Dashboard Builder | Creates analytics dashboards (retention, conversion, revenue) | Dashboard configs |
| Growth Analyst | Identifies growth levers and recommends experiments | `GROWTH_REPORT.md` |
| Experiment Designer | Designs A/B tests and growth experiments | `EXPERIMENTS.md` |

**Beads integration:** Issues per metric/dashboard. Experiment issues with hypothesis, success criteria, and results. Dependencies on Software Development for instrumentation.

**Why separate:** "What gets measured gets managed." Analytics is foundational to every other pipeline's success measurement, but requires specialized skills (statistical analysis, funnel optimization) that don't belong in any single pipeline.

---

### 12. Launch & Go-to-Market

**Purpose:** Coordinate all pipelines for a successful launch.

| Role | Description | Artifacts |
|------|-------------|-----------|
| Launch Coordinator | Creates launch checklist and timeline | `LAUNCH_PLAN.md` |
| Pre-launch Auditor | Validates all pipelines are launch-ready | `LAUNCH_READINESS.md` |
| Launch Executor | Executes launch sequence (DNS cutover, feature flags, announcements) | `LAUNCH_LOG.md` |
| Post-launch Monitor | Tracks launch metrics and flags critical issues | `POST_LAUNCH_REPORT.md` |

**Beads integration:** Meta-epic that depends on completion of all other pipeline epics. Launch checklist items as issues with hard dependencies.

**Why separate:** Launch is a coordination problem across all pipelines. It needs a dedicated pipeline to ensure nothing falls through the cracks — DNS propagation, payment testing in production, monitoring alerts configured, support team briefed, marketing campaigns scheduled.

---

## Meta-Orchestration: The CEO Agent

The CEO agent sits above all 12 pipelines. It is not a new agent type — it's the existing Kaggen coordinator with an expanded instruction set that includes pipeline sequencing logic.

### Responsibilities

1. **Pipeline sequencing** — Determines which pipelines to activate and in what order based on the user's request and current project state
2. **Go/no-go decisions** — Reviews pipeline outputs and decides whether to proceed, pivot, or kill the project
3. **Cross-pipeline dependency management** — Ensures pipeline outputs are available when downstream pipelines need them
4. **Resource allocation** — Decides how many pipelines to run in parallel vs. sequentially
5. **User communication** — Reports progress, surfaces decisions that need human input, and presents final results

### Decision Framework

```
User Request
     │
     ▼
┌─────────────┐     ┌──────────────────┐
│   Market     │────▶│   Competitive    │
│  Validation  │     │  Intelligence    │
└──────┬──────┘     └────────┬─────────┘
       │                     │
       ▼                     ▼
   GO/NO-GO ◀───────────────┘
       │
       ▼ (if GO)
┌──────┴──────┐
│   Legal &   │
│ Compliance  │
└──────┬──────┘
       │
       ▼
┌──────┴──────┐     ┌──────────────────┐
│   Design    │────▶│    Software      │
│   System    │     │   Development    │
└──────┬──────┘     └────────┬─────────┘
       │                     │
       ▼                     ▼
┌──────┴──────┐     ┌──────────────────┐
│  Payment &  │     │    DevOps /      │
│Monetization │     │ Infrastructure   │
└──────┬──────┘     └────────┬─────────┘
       │                     │
       ▼                     ▼
┌──────┴──────┐     ┌──────────────────┐
│  Content    │     │   Analytics &    │
│   & SEO     │     │    Growth        │
└──────┬──────┘     └────────┬─────────┘
       │                     │
       ▼                     ▼
┌──────┴──────┐     ┌──────────────────┐
│  Marketing  │     │    Customer      │
│  Campaign   │     │    Support       │
└──────┬──────┘     └────────┬─────────┘
       │                     │
       ▼                     ▼
       └────────┬────────────┘
                │
                ▼
        ┌───────┴───────┐
        │   Launch &    │
        │  Go-to-Market │
        └───────────────┘
```

### Parallel Execution Opportunities

Not all pipelines must be sequential. The CEO agent can run these in parallel:

- **Market Validation** ∥ **Competitive Intelligence** — independent research
- **Design System** ∥ **Legal & Compliance** — different domains, no dependencies
- **Content & SEO** ∥ **Marketing Campaign** — can draft in parallel, coordinate at launch
- **DevOps** ∥ **Analytics & Growth** — infrastructure and instrumentation are complementary
- **Customer Support** ∥ **Payment & Monetization** — independent implementation streams

---

## Shared Infrastructure

All 12 pipelines share the same foundational components:

### 1. Beads (Issue Tracking)

Every pipeline uses beads for work coordination:
- **Epics** per pipeline activation (e.g., "Market Validation for SaaS idea X")
- **Issues** per discrete work item with acceptance criteria
- **Dependencies** between issues across pipelines (e.g., Payment issues depend on Legal compliance issues)
- **Comments** for specs, findings, and review notes
- **Status tracking** — open → in_progress → closed (with reason)

### 2. Project Directory

All pipelines operate on a shared project directory under `/Users/stephanfeb/claude-projects/<project-name>/`:
- Each pipeline writes its artifacts to this directory
- Downstream pipelines read upstream artifacts as input
- Git versioning provides full history and rollback

### 3. Claude Code CLI Delegation

Every agent in every pipeline delegates work to `claude -p` with:
- `--add-dir <project-path>` for workspace context
- `--allowed-tools` restricted per role
- `--output-format json` for structured results
- `--dangerously-skip-permissions` for autonomous operation

### 4. Coordinator Pattern

Each pipeline follows the same coordinator → agent → Claude Code delegation pattern:
1. Coordinator receives request and identifies applicable pipeline
2. Coordinator dispatches pipeline stages sequentially via `dispatch_task`
3. Each stage agent delegates to Claude Code CLI via `exec`
4. Claude Code does the actual work (research, writing, coding, testing)
5. Results flow back through completion callbacks
6. Coordinator advances to the next stage or loops on failure

---

## The Generalization Insight

All 12 pipelines share identical structure:

```
Pipeline = {
    name: string,
    trigger: string,           // when to activate this pipeline
    stages: [
        {
            agent: string,     // skill name
            description: string,
            on_fail: string,   // optional: which stage to loop back to
            max_retries: int   // optional: max failure loops
        }
    ]
}
```

The only differences between pipelines are:
1. **Agent names** — which skills to dispatch
2. **Stage ordering** — which agents run in what sequence
3. **Failure policies** — which stage to retry on failure
4. **Trigger conditions** — when to activate the pipeline

This means pipelines can be defined declaratively in YAML config files rather than hardcoded in Go. Adding a new pipeline (e.g., "Podcast Production") requires only:
1. Creating skill SKILL.md files for the new roles
2. Writing a pipeline YAML file defining the stage sequence
3. Reloading via SIGHUP — no code changes, no recompilation

This is the path from "software development bot" to "autonomous business builder."
