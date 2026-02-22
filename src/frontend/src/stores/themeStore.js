import { create } from "zustand";
import { persist } from "zustand/middleware";

export const THEMES = [
  {
    id: "default",
    label: "Default",
    accent: "#f97316",
    desc: "Warm orange · system default",
  },
  {
    id: "phosphor",
    label: "Phosphor",
    accent: "#A3FF12",
    desc: "CRT terminal green · hacker",
  },
  {
    id: "obsidian",
    label: "Obsidian",
    accent: "#7C5CFC",
    desc: "Electric violet · premium",
  },
  {
    id: "infrared",
    label: "Infrared",
    accent: "#FF3D3D",
    desc: "Hot red · raw editorial",
  },
  {
    id: "fog",
    label: "Fog",
    accent: "#D4A843",
    desc: "Warm amber · lo-fi film",
  },
  {
    id: "void",
    label: "Void",
    accent: "#38BDF8",
    desc: "Ice blue · true black",
  },
];

const useThemeStore = create(
  persist(
    (set) => ({
      theme: "default",
      setTheme: (theme) => set({ theme }),
    }),
    { name: "wallium-theme" },
  ),
);

export default useThemeStore;
