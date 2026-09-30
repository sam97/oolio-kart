import type { Config } from "tailwindcss";

const config: Config = {
  content: [
    "./src/pages/**/*.{js,ts,jsx,tsx,mdx}",
    "./src/components/**/*.{js,ts,jsx,tsx,mdx}",
    "./src/app/**/*.{js,ts,jsx,tsx,mdx}",
  ],
  theme: {
    borderRadius: {
      s: "5px",
      m: "10px",
      lg: "20px",
    },
    extend: {
      colors: {
        primary: "#C73B0E",
        secondary: "#c66c50",
        grayBorder: "#B2A8A6",
        grayBg: "#FBF7F4",
        success: "#117E3E",
        error: "#971919",
      },
      fontFamily: {
        sans: ['"Red Hat Text"', "sans-serif"],
      },
      fontSize: {
        xxs: "12px",
        xs: "14px",
        s: "16px",
        m: "22px",
        l: "32px",
      },
      spacing: {
        xxs: "5px",
        xs: "8px",
        s: "10px",
        m: "20px",
        l: "25px",
        xl: "40px",
        xxl: "55px",
      },
    },
  },
  plugins: [],
};

export default config;
