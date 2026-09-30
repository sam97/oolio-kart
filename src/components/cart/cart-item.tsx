"use client";
import React from "react";
import ImageContainer from "../image";
import { createCartStore, ICart } from "@/stores/cart";

interface ICartItem {
  cart: ICart;
  showSmallAmount?: boolean;
  showDeleteIcon?: boolean;
}

export default function CartItem(props: ICartItem) {
  const { cart, showSmallAmount = true, showDeleteIcon } = props;
  const { decrementQty } = createCartStore();

  return (
    <div className="flex items-center border border-t-transparent border-l-transparent border-r-transparent">
      <div className="flex-1">
        <p className="label !text-[#716A67]">{cart.name}</p>

        {/* Quantity & Total */}
        <div className="flex pt-s py-s justify-start items-start">
          <p className="txt text-[#bc7964]">{cart.qty > 0 && cart.qty}</p>
          {showSmallAmount && <p className="label ml-s">@{cart.price}</p>}
          <p className="label ml-s">${cart.price * cart.qty}</p>
        </div>
      </div>
      {!showSmallAmount && <p className="title">${cart.price}</p>}
      {showDeleteIcon && (
        <ImageContainer
          className="cursor-pointer"
          src="/assets/delete.png"
          alt="delete-icon"
          onClickFunc={() => decrementQty(cart.id)}
        />
      )}
    </div>
  );
}
