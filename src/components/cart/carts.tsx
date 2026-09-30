"use client";
import React from "react";
import CartItem from "./cart-item";
import { createCartStore } from "@/stores/cart";
import ImageContainer from "../image";

interface ICart {
  showSmallAmount?: boolean;
  showDeleteIcon?: boolean;
}

export default function Carts(props: ICart) {
  const { showSmallAmount = false, showDeleteIcon = true } = props;
  const { carts, getTotalAmount } = createCartStore();

  return (
    <>
      {/* Empty Cart */}
      {Object.keys(carts).length === 0 && (
        <div className="mt-l flex flex-col items-center">
          <ImageContainer
            src="/assets/empty.png"
            height={100}
            width={120}
            alt="empty-cart-icon"
          />
          <p className="pt-l text-xs text-[#988A87]">
            Your added items will appear here
          </p>
        </div>
      )}
      <div className="mt-s overflow-y-auto max-h-[300px] scrollbar-hide">
        {Object.keys(carts).length > 0 &&
          Object.values(carts).map((cart, key) => (
            <div key={key}>
              <CartItem
                cart={cart}
                showSmallAmount={showSmallAmount}
                showDeleteIcon={showDeleteIcon}
              />
            </div>
          ))}
      </div>
      {/* Order Total */}
      {Object.keys(carts).length > 0 && (
        <div className="flex justify-between items-center mt-l">
          <p className="label txt-[#928D8B]">Order Total</p>
          <p className="total">${getTotalAmount()}</p>
        </div>
      )}
    </>
  );
}
