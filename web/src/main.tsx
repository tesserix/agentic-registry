import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter } from "react-router-dom";
import App from "./App";
import "./index.css";

// Apply the persisted/system theme before first paint (no flash).
const stored = localStorage.getItem("ar-theme");
const prefersDark = window.matchMedia("(prefers-color-scheme: dark)").matches;
if (stored ? stored === "dark" : prefersDark) {
  document.documentElement.classList.add("dark");
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <BrowserRouter>
      <App />
    </BrowserRouter>
  </StrictMode>,
);
