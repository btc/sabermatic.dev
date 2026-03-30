import asyncio
import json

from fastapi import APIRouter, WebSocket, WebSocketDisconnect

from backend.messages import WSMessage, parse_ws_message
from backend.orchestrator import InterviewOrchestrator, OrchestratorDeps
from backend.storage import SessionStorage

router = APIRouter()


@router.websocket("/ws/interview/{session_id}")
async def interview_websocket(ws: WebSocket, session_id: int):
    await ws.accept()
    deps = OrchestratorDeps(
        pool=ws.app.state.pool,
        anthropic_client=ws.app.state.anthropic_client,
        openai_client=ws.app.state.openai_client,
        settings=ws.app.state.settings,
        storage=SessionStorage(ws.app.state.settings.data_dir),
    )
    orch = InterviewOrchestrator(deps)

    async def send(data: dict):
        await ws.send_text(json.dumps(data, default=str))

    # Immediately enqueue load — orchestrator loads session from DB
    await orch.enqueue(WSMessage(type="load", session_id=session_id))
    runner = asyncio.create_task(orch.run(send))

    try:
        while True:
            raw = await ws.receive_text()
            msg = parse_ws_message(json.loads(raw))
            # TTS interrupt: handled outside the queue
            if msg.type == "audio":
                orch.cancel_tts()
                continue
            await orch.enqueue(msg)
    except WebSocketDisconnect:
        await orch.enqueue(WSMessage(type="shutdown"))
        await runner
