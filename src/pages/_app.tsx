"use client";
import type { AppProps } from "next/app";
import { ToastContainer } from "react-toastify";

// styles
import "@styles/index.scss";
import "react-toastify/dist/ReactToastify.css";

export default function MyApp({ Component, pageProps }: AppProps) {
  return (
    <>
      <div>
        <ToastContainer
          closeButton={true}
          autoClose={2000}
          position="top-center"
          hideProgressBar={false}
          pauseOnHover={true}
          closeOnClick={true}
          draggable={true}
        />
      </div>

      <Component {...pageProps} />
    </>
  );
}
