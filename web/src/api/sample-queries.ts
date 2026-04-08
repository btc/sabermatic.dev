import { useQuery } from "@connectrpc/connect-query";
import {
  getSampleSession,
  getSampleEvaluation,
  getSampleEducator,
  getSampleCoach,
} from "@/pb/drill/v1/sample-SampleService_connectquery";

export interface SampleQueryOptions {
  enabled?: boolean;
}

export function useSampleSession(options?: SampleQueryOptions) {
  return useQuery(getSampleSession, {}, {
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleEvaluation(options?: SampleQueryOptions) {
  return useQuery(getSampleEvaluation, {}, {
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleEducator(options?: SampleQueryOptions) {
  return useQuery(getSampleEducator, {}, {
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleCoach(options?: SampleQueryOptions) {
  return useQuery(getSampleCoach, {}, {
    staleTime: Infinity,
    enabled: options?.enabled,
  });
}
