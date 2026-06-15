type APIErrorShape = {
  response?: {
    data?: {
      error?: { message?: string };
      message?: string;
    };
  };
  message?: string;
};

export function extractApiErrorMessage(err: unknown): string | null {
  if (!err) return null;
  if (typeof err === 'string') return err;
  const obj = err as APIErrorShape;
  return obj.response?.data?.error?.message ?? obj.response?.data?.message ?? obj.message ?? null;
}
