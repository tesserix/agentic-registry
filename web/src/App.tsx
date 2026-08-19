import { Navigate, Route, Routes } from "react-router-dom";
import Shell from "./components/Shell";
import Catalog from "./pages/Catalog";
import ArtifactDetail from "./pages/ArtifactDetail";
import MCPGatewayShell from "./components/MCPGatewayShell";
import MCPGatewayAccess from "./pages/MCPGatewayAccess";
import MCPGatewayHome from "./pages/MCPGatewayHome";
import MCPGatewayServer from "./pages/MCPGatewayServer";
import { isMcpGatewayHost } from "./lib/mcpGateway";
import { RegistrySessionProvider } from "./lib/session";
import { isAgentGatewayHost } from "./lib/agentGateway";
import AgentGatewayShell from "./components/AgentGatewayShell";
import AgentGatewayHome from "./pages/AgentGatewayHome";

function AgentGatewayApp() {
  return (
    <RegistrySessionProvider>
      <AgentGatewayShell>
        <Routes>
          <Route path="/" element={<AgentGatewayHome />} />
          <Route path="*" element={<Navigate to="/" replace />} />
        </Routes>
      </AgentGatewayShell>
    </RegistrySessionProvider>
  );
}

function MCPGatewayApp() {
  return (
    <MCPGatewayShell>
      <Routes>
        <Route path="/" element={<MCPGatewayHome />} />
        <Route path="/servers/:name" element={<MCPGatewayServer />} />
        <Route path="/access" element={<MCPGatewayAccess />} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </MCPGatewayShell>
  );
}

export default function App() {
  const agentgatewayPreview = import.meta.env.VITE_AGENTGATEWAY_UI === "true";
  if (agentgatewayPreview || isAgentGatewayHost(window.location.hostname)) {
    return <AgentGatewayApp />;
  }
  const gatewayPreview = import.meta.env.VITE_MCP_GATEWAY_UI === "true";
  if (gatewayPreview || isMcpGatewayHost(window.location.hostname)) {
    return <MCPGatewayApp />;
  }

  return (
    <RegistrySessionProvider>
      <Shell>
        <Routes>
          <Route path="/" element={<Navigate to="/skills" replace />} />
          <Route path="/:plural" element={<Catalog />} />
          <Route path="/:plural/:name" element={<ArtifactDetail />} />
          <Route path="*" element={<Navigate to="/skills" replace />} />
        </Routes>
      </Shell>
    </RegistrySessionProvider>
  );
}
