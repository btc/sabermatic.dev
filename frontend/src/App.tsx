import { BrowserRouter, Routes, Route } from "react-router-dom";
import Home from "./pages/Home";
import Interview from "./pages/Interview";
import Results from "./pages/Results";
import History from "./pages/History";
import SessionReview from "./pages/SessionReview";

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Home />} />
        <Route path="/interview/:questionId" element={<Interview />} />
        <Route path="/results/:sessionId" element={<Results />} />
        <Route path="/history" element={<History />} />
        <Route path="/session/:sessionId" element={<SessionReview />} />
      </Routes>
    </BrowserRouter>
  );
}
