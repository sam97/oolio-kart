"use client";
import React from "react";
import { BottomSheet } from "react-spring-bottom-sheet";
import "react-spring-bottom-sheet/dist/style.css";

interface IBottomModal {
  open: boolean;
  setOpen: (value: boolean) => void;
  children: React.ReactNode;
  onCloseAction?: () => void;
  spaceDiscard?: boolean;
}

export const BottomModal = (props: IBottomModal) => {
  const {
    open,
    setOpen,
    children,
    onCloseAction = null,
    spaceDiscard = false,
  } = props;

  const dismiss = () => {
    onCloseAction && onCloseAction();
    setOpen(false);
  };

  return (
    <BottomSheet
      open={open}
      onDismiss={() => dismiss()}
      style={{
        pointerEvents: spaceDiscard ? "none" : "all",
      }}
    >
      <div
        style={{
          pointerEvents: "all",
        }}
      >
        {children}
      </div>
    </BottomSheet>
  );
};
