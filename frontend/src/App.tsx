import { BrowserRouter, Routes, Route } from "react-router-dom";
import Home from "./pages/Home";
import History from "./pages/History";
import SessionPage from "./pages/SessionPage";
import Learn from "./pages/Learn";

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Home />} />
        <Route path="/history" element={<History />} />
        <Route path="/sessions/:sessionId" element={<SessionPage />} />
        <Route path="/sessions/:sessionId/learn" element={<Learn />} />
      </Routes>
    </BrowserRouter>
  );
}
