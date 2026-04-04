import { useQuery } from "@tanstack/react-query";
import { apiClient } from "./client";
import type {
  Session, Message, EvaluationResponse,
  EducatorAnalysis, CoachAnalysis,
} from "./types";

interface SessionFixture {
  session: Session;
  messages: Message[];
}

export interface ScoreTrendPoint {
  date: string;
  overall_score: number;
}

export interface CoachFixture extends CoachAnalysis {
  score_trend: ScoreTrendPoint[];
}

interface SampleQueryOptions {
  enabled?: boolean;
}

export function useSampleSession(options?: SampleQueryOptions) {
  return useQuery({
    queryKey: ["sample", "session"],
    queryFn: () => apiClient.get<SessionFixture>("/api/sample/session"),
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleEvaluation(options?: SampleQueryOptions) {
  return useQuery({
    queryKey: ["sample", "evaluation"],
    queryFn: () => apiClient.get<EvaluationResponse>("/api/sample/evaluation"),
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleEducator(options?: SampleQueryOptions) {
  return useQuery({
    queryKey: ["sample", "educator"],
    queryFn: () => apiClient.get<EducatorAnalysis>("/api/sample/educator"),
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleCoach(options?: SampleQueryOptions) {
  return useQuery({
    queryKey: ["sample", "coach"],
    queryFn: () => apiClient.get<CoachFixture>("/api/sample/coach"),
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}
