export class ApiError extends Error {
  constructor(
    public status: number,
    public body: unknown,
  ) {
    super(`API error ${status}`);
    this.name = "ApiError";
  }
}

function getCsrfToken(): string {
  return (window as any).__csrfToken ?? "";
}

async function request<T>(url: string, options: RequestInit = {}): Promise<T> {
  const response = await fetch(url, {
    credentials: "same-origin",
    ...options,
  });
  // NOTE: `null as T` is a known type lie — all DELETE callers expect void/null
  // and there is no caller that reads the return value of a 204 response.
  if (response.status === 204) {
    return null as T;
  }
  // Read text first so the body stream is available for both JSON parse and
  // error reporting. Calling response.json() first consumes the stream, making
  // a subsequent response.text() call return an empty string.
  const text = await response.text();
  let data: unknown;
  try {
    data = JSON.parse(text);
  } catch {
    data = text || `Non-JSON response (${response.status})`;
  }
  if (!response.ok) {
    throw new ApiError(response.status, data);
  }
  return data as T;
}

function mutationHeaders(body?: unknown): HeadersInit {
  const headers: Record<string, string> = {
    "X-CSRF-Token": getCsrfToken(),
  };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  return headers;
}

export const apiClient = {
  get<T>(url: string): Promise<T> {
    return request<T>(url, { method: "GET" });
  },
  post<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "POST",
      headers: mutationHeaders(body),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  put<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "PUT",
      headers: mutationHeaders(body),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  patch<T>(url: string, body?: unknown): Promise<T> {
    return request<T>(url, {
      method: "PATCH",
      headers: mutationHeaders(body),
      body: body !== undefined ? JSON.stringify(body) : undefined,
    });
  },
  delete<T>(url: string): Promise<T> {
    return request<T>(url, {
      method: "DELETE",
      headers: mutationHeaders(),
    });
  },
};
