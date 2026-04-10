import { useQuery } from "@connectrpc/connect-query";

import {
  getSampleCoach,
  getSampleEducator,
  getSampleEvaluation,
  getSampleSession,
} from "@/pb/drill/v1/sample-SampleService_connectquery";

export interface SampleQueryOptions {
  enabled?: boolean;
}

export function useSampleSession(options?: SampleQueryOptions) {
  return useQuery(getSampleSession, {}, {
    staleTime: Infinity,
    gcTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleEvaluation(options?: SampleQueryOptions) {
  return useQuery(getSampleEvaluation, {}, {
    staleTime: Infinity,
    gcTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleEducator(options?: SampleQueryOptions) {
  return useQuery(getSampleEducator, {}, {
    staleTime: Infinity,
    gcTime: Infinity,
    enabled: options?.enabled,
  });
}

export function useSampleCoach(options?: SampleQueryOptions) {
  return useQuery(getSampleCoach, {}, {
    staleTime: Infinity,
    gcTime: Infinity,
    enabled: options?.enabled,
  });
}
