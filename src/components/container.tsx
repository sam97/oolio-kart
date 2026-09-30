"use client";
import React from "react";

interface IContainer {
  children: React.ReactNode;
}

export const Container = ({ children }: IContainer) => {
  return <div className="mx-auto min-h-screen">{children}</div>;
};
