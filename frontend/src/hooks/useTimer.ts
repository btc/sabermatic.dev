import { useCallback, useEffect, useRef, useState } from "react";

export function useTimer() {
  const [seconds, setSeconds] = useState(0);
  const startedAtRef = useRef<number | null>(null);

  const setStartedAt = useCallback((isoString: string) => {
    startedAtRef.current = new Date(isoString).getTime();
  }, []);

  useEffect(() => {
    const interval = setInterval(() => {
      if (startedAtRef.current) {
        setSeconds(Math.floor((Date.now() - startedAtRef.current) / 1000));
      }
    }, 1000);
    return () => clearInterval(interval);
  }, []);

  return { seconds, setStartedAt };
}
