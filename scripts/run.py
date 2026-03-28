"""Start both backend and frontend servers."""
import subprocess
import sys
import os
import webbrowser
import time


def main():
    root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

    # Ensure data directories exist
    os.makedirs(os.path.join(root, "data", "sessions"), exist_ok=True)
    os.makedirs(os.path.join(root, "data", "traces"), exist_ok=True)

    # Start backend
    backend = subprocess.Popen(
        [sys.executable, "-m", "uvicorn", "backend.main:app",
         "--host", "127.0.0.1", "--port", "8000", "--reload"],
        cwd=root,
    )

    # Start frontend
    frontend = subprocess.Popen(
        ["npm", "run", "dev"],
        cwd=os.path.join(root, "frontend"),
    )

    # Open browser
    time.sleep(3)
    webbrowser.open("http://localhost:5173")  # Vite default port

    try:
        backend.wait()
    except KeyboardInterrupt:
        backend.terminate()
        frontend.terminate()
        backend.wait()
        frontend.wait()


if __name__ == "__main__":
    main()
