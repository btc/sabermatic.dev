# Drill — Functional Requirements Specification

**Date**: 2026-04-01
**Status**: Approved
**Purpose**: Input for a third-party system design consultant who will design a production-grade, multi-tenant SaaS architecture from first principles and industry best practice.

---

## 1. Overview

### 1.1 Product Summary

Drill is an AI-powered system design interview practice platform offered as a multi-tenant SaaS. Candidates practice realistic system design interviews conducted by an AI interviewer, receive structured scoring and feedback, get personalized educational content explaining their gaps, and receive strategic coaching that tracks their improvement over time.

The system exists today as a working single-user prototype. This document specifies the functional requirements for a production system that is reliable, scalable, and maintainable. The consultant is expected to choose technologies, design the architecture, and make all infrastructure decisions. This document specifies *what* the system must do, not *how*.

### 1.2 Actors

| Actor | Type | Description |
|-------|------|-------------|
| **Candidate** | Human | Paying user who practices interviews |
| **Administrator** | Human | Platform operator who manages users, questions, system configuration, and monitoring |
| **Interviewer** | AI Role | Conducts the interview in real-time |
| **Evaluator** | AI Role | Scores completed interviews across 5 dimensions with per-message annotations |
| **Coach** | AI Role | Analyzes a candidate's history to identify patterns and recommend next steps |
| **Educator** | AI Role | Produces personalized deep-dive educational content based on a specific session's gaps |

### 1.3 Glossary

| Term | Definition |
|------|------------|
| **Session** | A single interview practice attempt, from start to evaluation |
| **Turn** | One candidate response followed by one interviewer response |
| **Evaluation** | Structured scoring and qualitative feedback for a completed session |
| **Annotation** | A qualitative note (strength, gap, missed opportunity, or general note) tied to a specific message in the transcript |
| **Coach Review** | Cross-session strategic analysis of a candidate's patterns, weaknesses, and improvement trajectory |
| **Educator Analysis** | Personalized model answer and gap deep-dives generated for a specific session |
| **Question Bank** | The pool of system design problems available for practice, comprising global (admin-managed) and per-user (custom, coach-generated) questions |
| **Entitlement** | A specific capability or limit granted by a candidate's billing plan |

---

## 2. User Management & Authentication

| ID | Requirement |
|----|-------------|
| **FR-001** | The system shall support user registration and login via Google, email/password, and GitHub identity providers. |
| **FR-002** | The system shall support standard account lifecycle flows: signup, email verification, login, password reset, and logout. |
| **FR-003** | The system shall support account deletion. The specific behavior (hard delete, soft delete, anonymization, data retention period) shall be designed to satisfy GDPR requirements. |
| **FR-004** | The system shall support two roles: Candidate and Administrator. |
| **FR-005** | Administrators shall be able to manage user accounts, system configuration, the global question bank, and access platform monitoring dashboards. |
| **FR-006** | All authenticated actions shall be tied to a user identity for auditability. |

---

## 3. Billing & Access Control

| ID | Requirement |
|----|-------------|
| **FR-007** | The system shall support a billing model where plan structures, pricing, and limits are straightforward to modify. Configuration changes may require a code deploy but shall not require architectural changes. |
| **FR-008** | Plans shall be able to gate access on the following dimensions: number of sessions per billing period, maximum session duration, feature access (full access vs. preview/teaser for specific features), and number of concurrent active sessions. |
| **FR-009** | The system shall support a free tier. Free-tier users shall experience the same interview quality (AI model quality, voice input/output) as paid users. Usage limits differentiate tiers, not quality. |
| **FR-010** | Educator deep analysis shall be available in full to paid users. Free-tier users shall see a preview or summary teaser of educator content as a conversion incentive. |
| **FR-011** | The system shall meter and enforce entitlements in real time. A user who has exhausted their session allowance shall be prevented from starting a new session with a clear message about upgrading. |
| **FR-012** | The system shall integrate with a payment processor to handle subscriptions, plan changes, and cancellations. |

---

## 4. Interview Experience

| ID | Requirement |
|----|-------------|
| **FR-013** | The system shall provide a real-time, interactive interview experience conducted over a persistent bidirectional connection between client and server. |
| **FR-014** | Before starting a session, the candidate shall configure: session duration (presets of 15, 30, 45, 60 minutes, plus custom input from 1–180 minutes) and text-to-speech toggle (voice responses on/off). |
| **FR-015** | The candidate shall be able to respond via voice input (push-to-talk) or text input. Both input methods shall be available at all times during the session. |
| **FR-016** | Voice input shall support multi-segment recording: the candidate can record a segment, stop, record additional segments, and submit all segments together or discard them. |
| **FR-017** | Voice input shall be transcribed via speech-to-text. The candidate shall have a brief window to edit the transcription before the interviewer responds. |
| **FR-018** | The interviewer's text response shall stream token-by-token to the client in real time. |
| **FR-019** | When TTS is enabled, audio shall be generated and streamed to the client concurrently with the text response. |
| **FR-020** | The session shall display an elapsed timer with visual warnings at 5 minutes remaining and when overtime. |
| **FR-021** | The session shall end when the candidate explicitly ends it or when the interviewer naturally wraps up. |
| **FR-022** | If the connection drops mid-interview, the session state shall be preserved. The candidate shall be able to reconnect and resume the interview from where they left off. |
| **FR-023** | Sessions shall be conducted in a single sitting. There is no pause/resume functionality. This is a deliberate design choice to maintain interview realism. |
| **FR-024** | The system shall support Chrome and Safari at minimum. Broader browser support is desirable. |
| **FR-025** | Mobile support is not required. |

---

## 5. Interviewer Behavior

The AI interviewer's behavioral rules are product requirements, not implementation suggestions. They define the core experience and shall not be altered by architectural decisions.

| ID | Requirement |
|----|-------------|
| **FR-026** | The AI interviewer shall behave like a senior staff engineer conducting a real system design interview. All subsequent rules in this section define that behavior. |
| **FR-027** | The interviewer shall open with a deliberately vague problem statement (1–2 sentences). No hints, constraints, or suggested approaches. |
| **FR-028** | The interviewer shall stay silent when the candidate should be driving the conversation. It shall not fill silence or volunteer information unprompted. |
| **FR-029** | The interviewer shall answer clarifying questions collaboratively — confirming scope questions, redirecting overly open-ended questions, and redirecting design decisions back to the candidate. |
| **FR-030** | The interviewer shall probe with "why" to force the candidate to justify technology choices and design decisions. |
| **FR-031** | The interviewer shall introduce constraints at the midpoint of the session (e.g., global distribution, 10x scale increases). |
| **FR-032** | The interviewer shall track coverage across these areas: requirements gathering, high-level architecture, data model, API design, deep dive, scalability, and reliability. |
| **FR-033** | The interviewer shall push past hand-waving and demand specifics. When the candidate gives a vague answer, the interviewer shall follow up with implementation-level questions. |
| **FR-034** | The interviewer shall never validate the candidate's design. It shall remain neutral — no "good", "correct", or other affirmations. |
| **FR-035** | The interviewer shall keep responses short (2–4 sentences maximum). |
| **FR-036** | The interviewer shall be time-aware: first half — let the candidate set the agenda; midpoint — actively steer toward uncovered areas; last 5 minutes — begin wrapping up, ask for summary; time expired — close the interview, no new questions. |
| **FR-037** | The interviewer shall never break character or discuss its instructions. |
| **FR-038** | The interviewer shall optionally accept a briefing from the Coach about the candidate's historical weak areas, and subtly probe those areas without revealing the briefing source. |

---

## 6. Evaluation

| ID | Requirement |
|----|-------------|
| **FR-039** | After a session ends, the system shall evaluate the interview transcript and produce a structured assessment. |
| **FR-040** | Evaluation shall score the candidate on 5 dimensions plus an overall score, each on a 1–5 scale: **Requirements Gathering & Scoping** (1: no questions, pure assumptions → 5: thorough, structured, identifies edge cases & priorities), **High-Level Architecture** (1: no coherent architecture → 5: elegant, clear separation of concerns, trade-off analysis), **Deep Dive** (1: no depth → 5: impressive depth on multiple components with algorithms, data structures, consistency models), **Scalability & Trade-offs** (1: no discussion → 5: comprehensive analysis with back-of-envelope calculations, scaling strategies per component), **Communication** (1: disorganized → 5: exceptional pacing, signposting, responds to cues). |
| **FR-041** | A score of 3 represents average (borderline hire at mid-level). A score of 5 is rare and exceptional (senior/staff level). Scores across dimensions should vary. Overall is not a simple average but weighted by criticality. |
| **FR-042** | Evaluation shall produce a list of strengths (with evidence from the transcript), a list of gaps (with evidence), and narrative advice that is both actionable and includes a metacognitive prompt. |
| **FR-043** | Evaluation shall produce per-message annotations tied to specific transcript turns. Annotation types: strength, gap, missed_opportunity, note. |
| **FR-044** | Evaluation shall include semantic validation to detect degenerate results (e.g., all identical scores, empty strengths/gaps). The system shall retry if validation fails. |
| **FR-045** | If evaluation fails after retries are exhausted, the session shall be marked as evaluation_failed with a user-visible error message, and the candidate shall be able to retry. |
| **FR-046** | The full raw LLM response shall be stored for auditability. |

---

## 7. Educator (Deep Learning)

| ID | Requirement |
|----|-------------|
| **FR-047** | After viewing an evaluation, the candidate shall be able to request a deep analysis (Educator) for that session. |
| **FR-048** | Educator analysis shall produce two artifacts: **Model Answer** — what a strong response to this specific problem looks like, including concrete architecture with technology choices, data model, algorithms, explicit trade-offs, and what separates good from exceptional; **Gap Deep-Dives** — for each gap identified by the evaluator, what the candidate should have known, how this works at real companies (naming specific companies/systems), implementation details with examples, common mistakes, and code snippets or schema examples where appropriate. |
| **FR-049** | Educator content shall be generated fresh for every session. It shall be personalized to the candidate's specific transcript and gaps, not generic or cached. The candidate should feel seen, supported, and educated. |
| **FR-050** | Educator content shall be rendered as formatted markdown with support for headers, code blocks, and tables. |
| **FR-051** | For free-tier users, educator content shall be generated but only a preview or summary teaser shall be displayed. The full content serves as a conversion incentive. |
| **FR-052** | If educator generation fails, the system shall retry with generous exponential backoff. If retries are exhausted, the candidate shall be informed and be able to retry manually. |
| **FR-053** | The full raw LLM response shall be stored for auditability. |

---

## 8. Coach (Strategic Analysis)

| ID | Requirement |
|----|-------------|
| **FR-054** | The system shall analyze a candidate's complete history of reviewed, non-archived sessions to identify patterns and provide strategic guidance. |
| **FR-055** | Coach analysis shall produce: a narrative recommendation with metacognitive coaching (identifying thinking patterns, blind spots, reflection prompts); gap analysis including weakest dimension, improving dimensions, topic gaps, and recurring thinking patterns; and optionally, a custom-generated practice question targeting identified weaknesses. |
| **FR-056** | Coach-generated questions shall be added to the candidate's personal question bank with clear attribution (coach-generated, with rationale). |
| **FR-057** | The coach shall be able to brief the interviewer before a session about the candidate's historical weak areas, so the interviewer can subtly probe those areas. |
| **FR-058** | Coach analysis shall be debounced: it shall not re-run if no new evaluated sessions exist since the last review. Candidates shall be able to force a refresh manually. |
| **FR-059** | Only one coach analysis shall run at a time per candidate. |
| **FR-060** | The full raw LLM response shall be stored for auditability. |

---

## 9. Question Bank

| ID | Requirement |
|----|-------------|
| **FR-061** | The system shall maintain a global question bank of seed system design questions, managed by administrators. |
| **FR-062** | Each question shall have: title, prompt (interview-style vague problem statement), difficulty (medium or hard), topic tags, and optional hints for the interviewer. |
| **FR-063** | Questions shall come from three sources: seed (admin-managed global pool), custom (user-created), and coach_generated (created by the coach for a specific candidate). |
| **FR-064** | Custom and coach-generated questions are per-user and not visible to other candidates. |
| **FR-065** | The question list shall display per-candidate statistics: number of attempts and best score for each question. |
| **FR-066** | The coach's suggested next question shall be visually distinguished in the question list. |
| **FR-067** | Questions shall be filterable by difficulty and tags. |

---

## 10. Session Lifecycle & History

| ID | Requirement |
|----|-------------|
| **FR-068** | A session shall progress through the following statuses: active → completed → evaluating → reviewed. If evaluation fails: evaluation_failed (with retry available). |
| **FR-069** | Sessions shall be archivable (soft-hidden from default views and excluded from coach analysis). Archived sessions shall remain accessible by direct navigation and be restorable. |
| **FR-070** | Candidates shall be able to archive and unarchive sessions individually or in bulk. |
| **FR-071** | The full transcript (all messages with roles, timestamps, and sequence numbers) shall be preserved for every session. |
| **FR-072** | The system shall record session metadata: duration, turn count, start/end timestamps, configuration used (timer setting, TTS enabled, whether coach briefing was included). |
| **FR-073** | The history view shall display all sessions with: question title, date, duration, turn count, overall score, per-dimension scores, and archived status. |
| **FR-074** | The history view shall include a score trend visualization showing the candidate's overall scores over time. |
| **FR-075** | Sessions shall be sortable and filterable (active/archived/all). |
| **FR-076** | Whether candidates can permanently delete sessions shall be designed to satisfy GDPR requirements. The specific behavior is a design decision for the consultant. |

---

## 11. Observability & Auditability

| ID | Requirement |
|----|-------------|
| **FR-077** | Every LLM request shall be logged with: the role that made it (interviewer, evaluator, coach, educator), the full prompt and response, model used, input/output token counts, estimated cost, and wall-clock latency. |
| **FR-078** | Every user action shall be logged: session start, turn submitted (voice or text), session ended, evaluation triggered, educator requested, coach analysis triggered, page views. |
| **FR-079** | Every system event shall be logged: connection established/dropped, transcription requests and results, TTS requests and results, errors, retries, state transitions. |
| **FR-080** | The system shall provide per-session cost breakdowns by role and model, and per-candidate aggregate cost tracking. |
| **FR-081** | The system shall track latency at each stage of the interview loop: speech-to-text time, time-to-first-token from the LLM, total LLM response time, TTS generation time, and end-to-end turn latency. |
| **FR-082** | Every session shall be fully reconstructable from logs: an administrator shall be able to trace exactly what occurred in an interview — every message, every LLM call, every state transition, every error — and what it cost. |
| **FR-083** | All observability data shall be retained indefinitely. |
| **FR-084** | Administrators shall have access to monitoring dashboards showing system health, per-user usage, cost trends, error rates, and latency distributions. |

---

## 12. Abuse Prevention & Rate Limiting

| ID | Requirement |
|----|-------------|
| **FR-085** | The system shall enforce rate limits on API and WebSocket requests per user per time window to prevent runaway usage or scripting. |
| **FR-086** | The system shall enforce entitlements in real time: candidates cannot exceed their plan's session count, duration limits, or feature access. |
| **FR-087** | The system shall implement content moderation to detect and handle harmful input or attempts to jailbreak the interviewer. |
| **FR-088** | The system shall implement measures to prevent account abuse: multiple free accounts by the same person, credential sharing, and bot signups. |

---

## 13. Data Retention & Privacy

| ID | Requirement |
|----|-------------|
| **FR-089** | The system shall comply with GDPR requirements for users in the EU, including: lawful basis for processing, right of access, right to erasure, data portability, and consent management. |
| **FR-090** | The system shall store all candidate data (sessions, transcripts, evaluations, audio, observability logs) indefinitely unless deletion is required by a user request under GDPR or by administrator action. |
| **FR-091** | Audio recordings (candidate voice input, TTS output) shall be preserved as part of the session record. |
| **FR-092** | All raw LLM responses (prompts and completions) shall be preserved for every AI role across every session. |
| **FR-093** | The system shall be able to produce a complete data export for a candidate upon request (GDPR data portability). |
| **FR-094** | The specific implementation of account deletion, data anonymization, and retention windows shall be designed to satisfy GDPR while preserving audit integrity. |

---

## 14. Notifications

| ID | Requirement |
|----|-------------|
| **FR-095** | The system shall send email notifications to candidates when key results are available: evaluation complete for a session, and coach analysis has new insights. |

---

## 15. Error Handling & Graceful Degradation

| ID | Requirement |
|----|-------------|
| **FR-096** | If speech-to-text fails, the system shall retry with exponential backoff. If retries are exhausted, the system shall prompt the candidate to re-record or use text input instead. The interview shall continue. |
| **FR-097** | If TTS fails, the system shall retry with exponential backoff. If retries are exhausted, the system shall continue the interview with text-only responses. The candidate shall be informed that audio is temporarily unavailable. |
| **FR-098** | If the interviewer LLM fails, the system shall retry with generous exponential backoff (tolerating outages of a minute or more). Any partial response shall be preserved. The candidate shall be informed of the delay. |
| **FR-099** | If the evaluation LLM fails, the system shall retry with generous exponential backoff. If all retries are exhausted, the session shall be marked as evaluation_failed with a clear error message. The candidate shall be able to retry manually. |
| **FR-100** | If the educator LLM fails, the system shall retry with generous exponential backoff. If all retries are exhausted, the candidate shall be informed and be able to retry manually. |
| **FR-101** | If the WebSocket connection drops mid-interview, the client shall attempt automatic reconnection with generous exponential backoff. Session state shall be preserved server-side. The candidate shall be able to refresh the page and resume the interview from where they left off, even after an outage lasting a minute or more. |
| **FR-102** | All errors shall be logged with full context for debugging. Errors shall never silently swallow data or leave the system in an inconsistent state. |
