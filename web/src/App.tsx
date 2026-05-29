import { Navigate, Route, Routes } from "react-router-dom";
import Shell from "./components/Shell";
import Catalog from "./pages/Catalog";
import ArtifactDetail from "./pages/ArtifactDetail";

export default function App() {
  return (
    <Shell>
      <Routes>
        <Route path="/" element={<Navigate to="/skills" replace />} />
        <Route path="/:plural" element={<Catalog />} />
        <Route path="/:plural/:name" element={<ArtifactDetail />} />
        <Route path="*" element={<Navigate to="/skills" replace />} />
      </Routes>
    </Shell>
  );
}
