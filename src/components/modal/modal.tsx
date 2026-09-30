"use client";
import React from "react";
const { Modal } = require("antd");

interface ICustomModal {
  children: React.ReactNode;
  centered?: boolean;
  open: boolean;
  title?: string;
  header?: string;
  width?: number;
  height?: number;
  footer?: null;
  okText: string;
  cancelText?: string;
  handleCancel?: () => void;
  onCallback?: () => void;
}

export const CustomModal = (props: ICustomModal) => {
  const {
    children,
    centered = true,
    open,
    title,
    header,
    width,
    height,
    footer = null,
    okText,
    cancelText,
    handleCancel,
    onCallback,
  } = props;

  const handleOk = (e: any) => {
    onCallback && onCallback();
  };

  return (
    <Modal
      centered={centered}
      open={open}
      title={title}
      okText={okText}
      cancelText={cancelText}
      onOk={(e: any) => handleOk(e)}
      onCancel={handleCancel}
      header={header}
      width={width}
      height={height}
      footer={footer}
    >
      {children}
    </Modal>
  );
};
